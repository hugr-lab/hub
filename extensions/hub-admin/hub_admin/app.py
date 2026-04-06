"""Server extension — checks admin flag via JupyterHub API server-side."""
import json
import logging
import os
import urllib.request

from jupyter_server.base.handlers import APIHandler
from jupyter_server.utils import url_path_join
import tornado.web

log = logging.getLogger("hub_admin")


class AdminCheckHandler(APIHandler):
    @tornado.web.authenticated
    def get(self):
        api_url = os.environ.get("JUPYTERHUB_API_URL", "")
        api_token = os.environ.get("JUPYTERHUB_API_TOKEN", "")
        user = os.environ.get("JUPYTERHUB_USER", "")

        if not api_url or not api_token or not user:
            log.warning("Missing JUPYTERHUB env vars, admin=false")
            self.finish(json.dumps({"admin": False}))
            return

        try:
            req = urllib.request.Request(
                f"{api_url}/users/{user}",
                headers={"Authorization": f"token {api_token}"},
            )
            with urllib.request.urlopen(req, timeout=5) as resp:
                data = json.loads(resp.read())
                is_admin = bool(data.get("admin"))
                log.info("Admin check for %s: %s", user, is_admin)
                self.finish(json.dumps({"admin": is_admin}))
        except Exception as e:
            log.warning("Admin check failed: %s", e)
            self.finish(json.dumps({"admin": False}))


def setup_handlers(web_app):
    host_pattern = ".*$"
    base_url = web_app.settings["base_url"]
    route = url_path_join(base_url, "hub-admin", "api", "check")
    web_app.add_handlers(host_pattern, [
        (route, AdminCheckHandler),
    ])


def _load_jupyter_server_extension(server_app):
    setup_handlers(server_app.web_app)
    server_app.log.info("hub_admin server extension loaded")


def _jupyter_server_extension_points():
    return [{"module": "hub_admin.app"}]
