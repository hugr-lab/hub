"""Server extension — WebSocket proxy for Hub Chat streaming.

REST proxies for conversations / agent instances / models have been removed:
the frontend now talks to Hugr GraphQL directly via hugr_connection_service.
What remains here is the WebSocket path to hub-service /ws/{conversation_id}
(chat streaming) and the small config helper that exposes the ws base URL.
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
