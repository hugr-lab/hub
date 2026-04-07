"""Server extension — WebSocket proxy to Hub Service for authenticated chat."""
import json
import logging
import os

from jupyter_server.base.handlers import JupyterHandler
from jupyter_server.utils import url_path_join
import tornado.web
import tornado.websocket
import tornado.httpclient
import tornado.ioloop

log = logging.getLogger("hub_chat")


class ChatWebSocketHandler(JupyterHandler, tornado.websocket.WebSocketHandler):
    """Proxy WebSocket: browser ↔ hub-service /ws/{user_id}."""

    upstream: tornado.websocket.WebSocketClientConnection | None = None

    def check_origin(self, origin):
        return True

    @tornado.web.authenticated
    async def get(self, *args, **kwargs):
        # Tornado WebSocket upgrade needs to go through get()
        return await super().get(*args, **kwargs)

    async def open(self):
        hub_service_url = os.environ.get("HUB_SERVICE_URL", "")
        user = os.environ.get("JUPYTERHUB_USER", "")

        if not hub_service_url or not user:
            log.warning("HUB_SERVICE_URL or JUPYTERHUB_USER not set")
            self.close(1011, "Hub service not configured")
            return

        # Connect to upstream hub-service WebSocket
        ws_url = hub_service_url.replace("http://", "ws://").replace("https://", "wss://")
        ws_url = f"{ws_url}/ws/{user}"

        log.info("Connecting to upstream: %s", ws_url)

        try:
            self.upstream = await tornado.websocket.websocket_connect(
                ws_url,
                on_message_callback=self._on_upstream_message,
            )
            log.info("Connected to hub-service WebSocket for user %s", user)
        except Exception as e:
            log.error("Failed to connect to hub-service: %s", e)
            self.close(1011, f"Upstream connection failed: {e}")

    def _on_upstream_message(self, message):
        """Forward message from hub-service → browser."""
        if message is None:
            # Upstream closed
            self.close()
            return
        try:
            self.write_message(message)
        except tornado.websocket.WebSocketClosedError:
            pass

    def on_message(self, message):
        """Forward message from browser → hub-service."""
        if self.upstream:
            self.upstream.write_message(message)

    def on_close(self):
        """Close upstream when browser disconnects."""
        if self.upstream:
            self.upstream.close()
            self.upstream = None
        log.info("Chat WebSocket closed")


class ChatConfigHandler(JupyterHandler):
    """GET /hub-chat/api/config — return WebSocket URL for the frontend."""

    @tornado.web.authenticated
    def get(self):
        base_url = self.settings.get("base_url", "/")
        ws_path = url_path_join(base_url, "hub-chat", "ws")
        # Build full WebSocket URL from request
        protocol = "wss" if self.request.protocol == "https" else "ws"
        host = self.request.host
        ws_url = f"{protocol}://{host}{ws_path}"
        self.finish(json.dumps({"ws_url": ws_url}))


def setup_handlers(web_app):
    host_pattern = ".*$"
    base_url = web_app.settings["base_url"]
    web_app.add_handlers(host_pattern, [
        (url_path_join(base_url, "hub-chat", "ws"), ChatWebSocketHandler),
        (url_path_join(base_url, "hub-chat", "api", "config"), ChatConfigHandler),
    ])


def _load_jupyter_server_extension(server_app):
    setup_handlers(server_app.web_app)
    server_app.log.info("hub_chat server extension loaded")


def _jupyter_server_extension_points():
    return [{"module": "hub_chat.app"}]
