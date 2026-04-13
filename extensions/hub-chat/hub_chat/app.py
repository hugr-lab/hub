"""Server extension — WebSocket proxy for Hub Chat streaming.

Routes conversations to the right backend:
- mode=llm (Quick Chat): hub-service /ws/{conversation_id}
- mode=agent, runtime_context=local: workspace hub-agent on localhost:18888
- mode=agent, runtime_context=remote: hub-service /ws/{conversation_id}

The agent-bridge extension manages the workspace agent process (lazy start).
"""
import json
import logging
import os

from jupyter_server.base.handlers import JupyterHandler
from jupyter_server.utils import url_path_join
import tornado.web
import tornado.websocket
import tornado.httpclient

log = logging.getLogger("hub_chat")

AGENT_LISTEN = os.environ.get("HUB_AGENT_LISTEN", "localhost:18888")

# Cache: conversation_id → runtime_context (doesn't change after creation)
_context_cache: dict[str, str] = {}


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


async def _resolve_conversation_context(conversation_id: str, token: str | None) -> str:
    """Determine runtime_context for a conversation by querying its agent_type.

    Returns 'local', 'remote', or 'llm'.
    Caches result per conversation_id (runtime_context doesn't change).
    """
    if conversation_id in _context_cache:
        return _context_cache[conversation_id]

    hub_service_url = os.environ.get("HUB_SERVICE_URL", "")
    if not hub_service_url or not token:
        return "remote"  # fallback

    try:
        client = tornado.httpclient.AsyncHTTPClient()
        query = json.dumps({"query": f'''{{
            hub {{ db {{ conversations(filter: {{id: {{eq: "{conversation_id}"}}}}, limit: 1) {{
                mode
                agent {{ agent_type {{ runtime_context }} }}
            }} }} }}
        }}'''})
        resp = await client.fetch(
            f"{hub_service_url}/hugr",
            method="POST",
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {token}",
            },
            body=query,
            request_timeout=5,
        )
        data = json.loads(resp.body)
        convs = data.get("data", {}).get("hub", {}).get("db", {}).get("conversations", [])
        if not convs:
            return "remote"

        conv = convs[0]
        mode = conv.get("mode", "")
        if mode == "llm":
            _context_cache[conversation_id] = "llm"
            return "llm"

        agent = conv.get("agent") or {}
        agent_type = agent.get("agent_type") or {}
        runtime_ctx = agent_type.get("runtime_context", "")
        result = "local" if runtime_ctx == "local" else "remote"
        _context_cache[conversation_id] = result
        return result
    except Exception as e:
        log.warning("Failed to resolve conversation context: %s", e)
        return "remote"


class ChatWebSocketHandler(JupyterHandler, tornado.websocket.WebSocketHandler):
    """Proxy WebSocket: browser ↔ backend (hub-service or workspace agent)."""

    upstream: tornado.websocket.WebSocketClientConnection | None = None

    def check_origin(self, origin):
        return True

    @tornado.web.authenticated
    async def get(self, *args, **kwargs):
        return await super().get(*args, **kwargs)

    async def open(self, conversation_id: str = ""):
        if not conversation_id:
            self.close(1011, "conversation_id required")
            return

        token = _get_access_token()
        runtime_ctx = await _resolve_conversation_context(conversation_id, token)

        if runtime_ctx == "local":
            await self._connect_local_agent(conversation_id, token)
        else:
            await self._connect_hub_service(conversation_id, token)

    async def _connect_local_agent(self, conversation_id: str, token: str | None):
        """Route to workspace hub-agent on localhost."""
        # Lazy-start agent via agent-bridge
        try:
            from agent_bridge.app import ensure_agent_running
            ready = await ensure_agent_running(access_token=token)
            if not ready:
                self.close(1011, "Workspace agent not available (permission denied or startup failed)")
                return
        except ImportError:
            log.warning("agent_bridge not installed, cannot start local agent")
            self.close(1011, "agent-bridge extension not available")
            return

        ws_url = f"ws://{AGENT_LISTEN}/ws/{conversation_id}"
        log.info("Connecting to workspace agent: %s", ws_url)

        try:
            self.upstream = await tornado.websocket.websocket_connect(
                ws_url,
                on_message_callback=self._on_upstream_message,
            )
            log.info("Connected to workspace agent for conversation %s", conversation_id)
        except Exception as e:
            log.error("Failed to connect to workspace agent: %s", e)
            self.close(1011, f"Workspace agent connection failed: {e}")

    async def _connect_hub_service(self, conversation_id: str, token: str | None):
        """Route to hub-service (Quick Chat / remote agents)."""
        hub_service_url = os.environ.get("HUB_SERVICE_URL", "")
        if not hub_service_url:
            self.close(1011, "Hub service not configured")
            return

        ws_url = hub_service_url.replace("http://", "ws://").replace("https://", "wss://")
        ws_url = f"{ws_url}/ws/{conversation_id}"

        log.info("Connecting to hub-service: %s", ws_url)

        try:
            headers = {}
            if token:
                headers["Authorization"] = f"Bearer {token}"
            request = tornado.httpclient.HTTPRequest(ws_url, headers=headers)
            self.upstream = await tornado.websocket.websocket_connect(
                request,
                on_message_callback=self._on_upstream_message,
            )
            log.info("Connected to hub-service for conversation %s", conversation_id)
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
    ])


def _load_jupyter_server_extension(server_app):
    setup_handlers(server_app.web_app)
    server_app.log.info("hub_chat server extension loaded")


def _jupyter_server_extension_points():
    return [{"module": "hub_chat.app"}]
