"""WebSocket relay handler for agent-bridge.

Proxies WebSocket frames between the browser and the local hub-agent
at localhost:18888. Pure relay — no message transformation.
"""

import os
import logging
import tornado.websocket
import tornado.httpclient

logger = logging.getLogger("agent_bridge")

AGENT_URL = os.environ.get("HUB_AGENT_URL", "ws://localhost:18888")


class AgentBridgeWebSocketHandler(tornado.websocket.WebSocketHandler):
    """Relay WS frames between browser and local hub-agent."""

    upstream: tornado.websocket.WebSocketClientConnection | None = None

    def check_origin(self, origin):
        return True  # local only — jupyter-server auth handles access

    async def open(self, conversation_id: str):
        """Connect to local hub-agent when browser opens WS."""
        agent_ws_url = f"{AGENT_URL}/ws/{conversation_id}"
        logger.info(f"agent-bridge: connecting to {agent_ws_url}")

        try:
            self.upstream = await tornado.websocket.websocket_connect(
                agent_ws_url,
                on_message_callback=self._on_upstream_message,
            )
        except Exception as e:
            logger.error(f"agent-bridge: failed to connect to agent: {e}")
            self.close(1011, f"agent unavailable: {e}")
            return

        logger.info(f"agent-bridge: connected for {conversation_id}")

    def on_message(self, message):
        """Forward browser message to agent."""
        if self.upstream and not self.upstream.protocol is None:
            self.upstream.write_message(message)

    def _on_upstream_message(self, message):
        """Forward agent message to browser."""
        if message is None:
            # Agent closed connection
            self.close()
            return
        try:
            self.write_message(message)
        except tornado.websocket.WebSocketClosedError:
            pass

    def on_close(self):
        """Clean up upstream connection."""
        if self.upstream:
            self.upstream.close()
            self.upstream = None
        logger.info("agent-bridge: disconnected")


def setup_handlers(web_app):
    """Register the agent-bridge WebSocket handler."""
    base_url = web_app.settings.get("base_url", "/")
    route = f"{base_url}agent-bridge/ws/(.*)"
    web_app.add_handlers(".*$", [(route, AgentBridgeWebSocketHandler)])
    logger.info(f"agent-bridge: registered at {route}")
