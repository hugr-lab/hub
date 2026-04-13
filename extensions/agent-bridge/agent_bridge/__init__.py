"""Agent Bridge — JupyterLab server extension that relays WebSocket
connections from the browser to the local workspace hub-agent."""

from .app import setup_handlers


def _jupyter_server_extension_points():
    return [{"module": "agent_bridge"}]


def _load_jupyter_server_extension(server_app):
    setup_handlers(server_app.web_app)
    server_app.log.info("agent-bridge extension loaded")
