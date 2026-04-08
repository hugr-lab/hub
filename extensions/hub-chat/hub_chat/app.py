"""Server extension — WebSocket proxy and conversation API proxy for Hub Chat."""
import json
import logging
import os

from jupyter_server.base.handlers import JupyterHandler
from jupyter_server.utils import url_path_join
import tornado.web
import tornado.websocket
import tornado.httpclient

log = logging.getLogger("hub_chat")


def _get_access_token() -> str | None:
    """Get current OIDC access token from hub_token_provider in-memory store."""
    try:
        from hugr_connection_service.hub_token_provider import get_hub_token
        connection_name = os.environ.get("HUGR_CONNECTION_NAME", "default")
        token_data = get_hub_token(connection_name)
        if token_data:
            return token_data.get("access_token")
    except ImportError:
        log.debug("hugr_connection_service not available")
    return None


class ChatWebSocketHandler(JupyterHandler, tornado.websocket.WebSocketHandler):
    """Proxy WebSocket: browser ↔ hub-service /ws/{conversation_id}."""

    upstream: tornado.websocket.WebSocketClientConnection | None = None

    def check_origin(self, origin):
        return True

    @tornado.web.authenticated
    async def get(self, *args, **kwargs):
        return await super().get(*args, **kwargs)

    async def open(self, conversation_id: str = ""):
        hub_service_url = os.environ.get("HUB_SERVICE_URL", "")
        if not hub_service_url or not conversation_id:
            log.warning("HUB_SERVICE_URL or conversation_id not set")
            self.close(1011, "Hub service not configured")
            return

        token = _get_access_token()

        ws_url = hub_service_url.replace("http://", "ws://").replace("https://", "wss://")
        ws_url = f"{ws_url}/ws/{conversation_id}"

        log.info("Connecting to upstream: %s (token: %s)", ws_url, "yes" if token else "no")

        try:
            headers = {}
            if token:
                headers["Authorization"] = f"Bearer {token}"
            request = tornado.httpclient.HTTPRequest(ws_url, headers=headers)
            self.upstream = await tornado.websocket.websocket_connect(
                request,
                on_message_callback=self._on_upstream_message,
            )
            log.info("Connected to hub-service WebSocket for conversation %s", conversation_id)
        except Exception as e:
            log.error("Failed to connect to hub-service: %s", e)
            self.close(1011, f"Upstream connection failed: {e}")

    def _on_upstream_message(self, message):
        if message is None:
            self.close()
            return
        try:
            self.write_message(message)
        except tornado.websocket.WebSocketClosedError:
            pass

    def on_message(self, message):
        if self.upstream:
            self.upstream.write_message(message)

    def on_close(self):
        if self.upstream:
            self.upstream.close()
            self.upstream = None
        log.info("Chat WebSocket closed")


class ConversationAPIHandler(JupyterHandler):
    """Proxy REST: browser → Hugr GraphQL for conversation CRUD."""

    @tornado.web.authenticated
    async def post(self, action: str):
        body = json.loads(self.request.body) if self.request.body else {}
        user = os.environ.get("JUPYTERHUB_USER", "")
        token = _get_access_token()

        # Map action to GraphQL query via hugr connection proxy
        gql, variables = self._build_query(action, body, user)
        if not gql:
            self.set_status(400)
            self.finish(json.dumps({"error": f"unknown action: {action}"}))
            return

        result = await self._hugr_query(gql, variables, token)
        self.set_header("Content-Type", "application/json")
        self.finish(json.dumps(result))

    def _build_query(self, action, body, user):
        import time
        if action == "create":
            conv_id = f"conv-{int(time.time() * 1000)}"
            mode = body.get("mode", "tools")
            title = body.get("title", "New Chat")
            return (
                'mutation($id: String!, $uid: String!, $title: String!, $mode: String!) { hub { db { insert_conversations(data: { id: $id, user_id: $uid, title: $title, mode: $mode }) { id title mode } } } }',
                {"id": conv_id, "uid": user, "title": title, "mode": mode},
            )
        if action == "list":
            return (
                'query($uid: String!) { hub { db { conversations(filter: { user_id: { eq: $uid }, deleted_at: { is_null: true } }, order_by: [{field: "updated_at", direction: DESC}]) { id title folder mode agent_instance_id model updated_at created_at } } } }',
                {"uid": user},
            )
        if action == "rename":
            return (
                'mutation($id: String!, $title: String!) { hub { db { update_conversations(filter: { id: { eq: $id } }, data: { title: $title }) { affected_rows } } } }',
                {"id": body.get("id", ""), "title": body.get("title", "")},
            )
        if action == "delete":
            return (
                'mutation($id: String!) { hub { db { update_conversations(filter: { id: { eq: $id } }, data: { deleted_at: "NOW()" }) { affected_rows } } } }',
                {"id": body.get("id", "")},
            )
        if action == "messages":
            limit = body.get("limit", 50)
            conv_id = body.get("id", "")
            return (
                'query($cid: String!, $limit: Int!) { hub { db { agent_messages(filter: { conversation_id: { eq: $cid } }, order_by: [{field: "created_at", direction: DESC}], limit: $limit) { id role content tool_calls tool_call_id tokens_used model created_at } } } }',
                {"cid": conv_id, "limit": limit},
            )
        return None, None

    async def _hugr_query(self, gql, variables, token):
        """Execute GraphQL via Hugr IPC endpoint with OIDC token."""
        # Get Hugr URL from connection config
        hugr_url = self._get_hugr_url()
        if not hugr_url:
            return {"error": "Hugr URL not configured"}

        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"

        client = tornado.httpclient.AsyncHTTPClient()
        try:
            response = await client.fetch(
                hugr_url,
                method="POST",
                headers=headers,
                body=json.dumps({"query": gql, "variables": variables}),
                request_timeout=30,
            )
            resp_body = response.body.decode()

            # Hugr may return multipart/mixed (IPC) or JSON
            if resp_body.startswith("--HUGR"):
                # Parse multipart: extract JSON part
                result = self._parse_hugr_multipart(resp_body)
            else:
                result = json.loads(resp_body)

            data = result.get("data", result)
            # Extract from nested hub.db structure
            if isinstance(data, dict) and "hub" in data:
                data = data["hub"]
            if isinstance(data, dict) and "db" in data:
                data = data["db"]
            # Return the first meaningful value
            if isinstance(data, dict):
                for k, v in data.items():
                    return v
            return data
        except tornado.httpclient.HTTPClientError as e:
            body_text = e.response.body.decode() if e.response and e.response.body else str(e)
            return {"error": body_text}
        except Exception as e:
            return {"error": str(e)}

    def _get_hugr_url(self):
        """Get Hugr IPC URL from connections.json."""
        import pathlib
        config_path = os.environ.get("HUGR_CONFIG_PATH", str(pathlib.Path.home() / ".hugr" / "connections.json"))
        try:
            with open(config_path) as f:
                cfg = json.load(f)
            default_name = cfg.get("default", "default")
            for conn in cfg.get("connections", []):
                if conn.get("name") == default_name:
                    return conn.get("url", "").rstrip("/")
        except Exception:
            pass
        hugr_url = os.environ.get("HUGR_URL", "")
        if hugr_url:
            return hugr_url.rstrip("/")
        return None

    def _parse_hugr_multipart(self, body):
        """Parse simple HUGR multipart response to extract JSON data."""
        result = {}
        parts = body.split("--HUGR")
        for part in parts:
            if "X-Hugr-Part-Type: data" in part:
                # Extract JSON body after headers
                lines = part.strip().split("\n\n", 1)
                if len(lines) > 1:
                    try:
                        data = json.loads(lines[1].strip())
                        # Check for path header
                        if "X-Hugr-Path:" in lines[0]:
                            path_line = [l for l in lines[0].split("\n") if "X-Hugr-Path:" in l]
                            if path_line:
                                path = path_line[0].split(":", 1)[1].strip().replace("data.", "")
                                # Set nested
                                parts_list = path.split(".")
                                current = result
                                for p in parts_list[:-1]:
                                    current.setdefault(p, {})
                                    current = current[p]
                                current[parts_list[-1]] = data
                    except json.JSONDecodeError:
                        pass
            elif "X-Hugr-Part-Type: errors" in part:
                lines = part.strip().split("\n\n", 1)
                if len(lines) > 1:
                    try:
                        result["errors"] = json.loads(lines[1].strip())
                    except json.JSONDecodeError:
                        pass
        return {"data": result} if result else {}


class ChatConfigHandler(JupyterHandler):
    """GET /hub-chat/api/config — return WebSocket URL pattern for frontend."""

    @tornado.web.authenticated
    def get(self):
        base_url = self.settings.get("base_url", "/")
        ws_base = url_path_join(base_url, "hub-chat", "ws")
        protocol = "wss" if self.request.protocol == "https" else "ws"
        host = self.request.host
        self.finish(json.dumps({
            "ws_base": f"{protocol}://{host}{ws_base}",
        }))


def setup_handlers(web_app):
    host_pattern = ".*$"
    base_url = web_app.settings["base_url"]
    web_app.add_handlers(host_pattern, [
        (url_path_join(base_url, "hub-chat", "ws", "(.*)"), ChatWebSocketHandler),
        (url_path_join(base_url, "hub-chat", "api", "config"), ChatConfigHandler),
        (url_path_join(base_url, "hub-chat", "api", "conversations", "(.*)"), ConversationAPIHandler),
    ])


def _load_jupyter_server_extension(server_app):
    setup_handlers(server_app.web_app)
    server_app.log.info("hub_chat server extension loaded")


def _jupyter_server_extension_points():
    return [{"module": "hub_chat.app"}]
