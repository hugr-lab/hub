"""Proxy handler for Hub Service API calls from admin panel.
Routes /hub-admin/api/hub/* → HUB_SERVICE_URL/* with OIDC Bearer token."""
import json
import logging
import os

from jupyter_server.base.handlers import JupyterHandler
from jupyter_server.utils import url_path_join
import tornado.web
import tornado.httpclient

log = logging.getLogger("hub_admin.hub_proxy")


class HubServiceProxyHandler(JupyterHandler):
    """Proxy POST/GET requests to Hub Service with OIDC auth."""

    @tornado.web.authenticated
    async def post(self, path: str):
        await self._proxy("POST", path)

    @tornado.web.authenticated
    async def get(self, path: str):
        await self._proxy("GET", path)

    async def _proxy(self, method: str, path: str):
        hub_service_url = os.environ.get("HUB_SERVICE_URL", "")
        if not hub_service_url:
            self.set_status(503)
            self.finish(json.dumps({"error": "HUB_SERVICE_URL not configured"}))
            return

        token = self._get_access_token()
        url = f"{hub_service_url}/{path.lstrip('/')}"

        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"

        client = tornado.httpclient.AsyncHTTPClient()
        try:
            body = self.request.body if method == "POST" else None
            response = await client.fetch(
                url,
                method=method,
                headers=headers,
                body=body,
                request_timeout=30,
                allow_nonstandard_methods=True,
            )
            self.set_status(response.code)
            self.set_header("Content-Type", "application/json")
            self.finish(response.body)
        except tornado.httpclient.HTTPClientError as e:
            if e.response:
                self.set_status(e.response.code)
                self.finish(e.response.body)
            else:
                self.set_status(502)
                self.finish(json.dumps({"error": str(e)}))
        except Exception as e:
            self.set_status(502)
            self.finish(json.dumps({"error": str(e)}))

    def _get_access_token(self) -> str | None:
        """Get current OIDC access token from hub_token_provider."""
        try:
            from hugr_connection_service.hub_token_provider import get_hub_token
            connection_name = os.environ.get("HUGR_CONNECTION_NAME", "default")
            token_data = get_hub_token(connection_name)
            if token_data:
                return token_data.get("access_token")
        except ImportError:
            log.debug("hugr_connection_service not available")
        return None


def setup_handlers(web_app):
    host_pattern = ".*$"
    base_url = web_app.settings["base_url"]
    route = url_path_join(base_url, "hub-admin", "api", "hub", "(.*)")
    web_app.add_handlers(host_pattern, [
        (route, HubServiceProxyHandler),
    ])
