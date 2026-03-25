"""
JupyterHub configuration for Analytics Hub.

OIDC endpoints are auto-discovered from Hugr's /auth/config endpoint.
Only three env vars required: HUGR_URL, OIDC_CLIENT_SECRET, HUB_BASE_URL.
"""

import os
import time
import logging

import httpx
from oauthenticator.generic import GenericOAuthenticator
from dockerspawner import DockerSpawner

logger = logging.getLogger(__name__)

# ===========================================================================
# Required configuration
# ===========================================================================

HUGR_URL = os.environ["HUGR_URL"]  # e.g. http://hugr:15000
HUB_BASE_URL = os.environ["HUB_BASE_URL"]  # e.g. http://localhost:8000

# ===========================================================================
# OIDC Auto-Discovery from Hugr
# ===========================================================================


def discover_oidc(max_retries=30, initial_delay=2, max_delay=60):
    """Discover OIDC configuration from Hugr server.

    Retries with exponential backoff if Hugr is not yet available.
    """
    delay = initial_delay

    for attempt in range(1, max_retries + 1):
        try:
            # Step 1: Get OIDC params from Hugr
            resp = httpx.get(f"{HUGR_URL}/auth/config", timeout=10)
            resp.raise_for_status()
            hugr_auth = resp.json()

            # Allow override via env (needed when Hugr returns localhost
            # but Hub runs in a different container)
            issuer = os.environ.get("OIDC_ISSUER", hugr_auth["issuer"])
            client_id = os.environ.get(
                "OIDC_CLIENT_ID", hugr_auth.get("client_id", "hugr")
            )
            scopes = list(hugr_auth.get("scopes", ["openid", "profile"]))

            # Ensure required scopes
            for scope in ["offline_access", "email"]:
                if scope not in scopes:
                    scopes.append(scope)

            # Extra scopes from env (e.g., Entra API scope for correct audience)
            extra_scopes = os.environ.get("OIDC_EXTRA_SCOPES", "")
            if extra_scopes:
                for s in extra_scopes.split(","):
                    s = s.strip()
                    if s and s not in scopes:
                        scopes.append(s)

            # Step 2: Discover OIDC endpoints from provider
            oidc_resp = httpx.get(
                f"{issuer}/.well-known/openid-configuration", timeout=10
            )
            oidc_resp.raise_for_status()
            oidc_config = oidc_resp.json()

            logger.info(
                "OIDC discovery successful: issuer=%s, client_id=%s", issuer, client_id
            )

            return {
                "issuer": issuer,
                "client_id": client_id,
                "scopes": scopes,
                "authorize_url": oidc_config["authorization_endpoint"],
                "token_url": oidc_config["token_endpoint"],
                "userinfo_url": oidc_config["userinfo_endpoint"],
            }

        except Exception as e:
            if attempt == max_retries:
                logger.error(
                    "OIDC discovery failed after %d attempts: %s", max_retries, e
                )
                raise RuntimeError(
                    f"Failed to discover OIDC config from {HUGR_URL}/auth/config "
                    f"after {max_retries} attempts: {e}"
                ) from e

            logger.warning(
                "OIDC discovery attempt %d/%d failed: %s. Retrying in %ds...",
                attempt,
                max_retries,
                e,
                delay,
            )
            time.sleep(delay)
            delay = min(delay * 2, max_delay)


oidc = discover_oidc()

# ===========================================================================
# Authenticator
# ===========================================================================

c.JupyterHub.authenticator_class = GenericOAuthenticator

c.GenericOAuthenticator.oauth_callback_url = f"{HUB_BASE_URL}/hub/oauth_callback"
c.GenericOAuthenticator.authorize_url = oidc["authorize_url"]
c.GenericOAuthenticator.token_url = oidc["token_url"]
# Entra with custom API scope: userinfo endpoint requires Graph token which
# conflicts with our API audience. Use id_token claims instead.
if os.environ.get("OIDC_USERDATA_FROM_ID_TOKEN"):
    c.GenericOAuthenticator.userdata_from_id_token = True
else:
    c.GenericOAuthenticator.userdata_url = oidc["userinfo_url"]
c.GenericOAuthenticator.client_id = oidc["client_id"]
c.GenericOAuthenticator.client_secret = os.environ.get("OIDC_CLIENT_SECRET", "")
c.GenericOAuthenticator.scope = oidc["scopes"]
c.GenericOAuthenticator.login_service = "Hugr SSO"

c.GenericOAuthenticator.enable_auth_state = True
c.GenericOAuthenticator.refresh_pre_spawn = True
c.GenericOAuthenticator.auth_refresh_age = 120
c.GenericOAuthenticator.username_claim = os.environ.get(
    "OIDC_USERNAME_CLAIM", "preferred_username"
)
c.GenericOAuthenticator.allow_all = True

# ===========================================================================
# Spawner
# ===========================================================================

c.JupyterHub.spawner_class = DockerSpawner

c.DockerSpawner.image = os.environ.get(
    "SINGLEUSER_IMAGE", "hugr-lab/hub-singleuser:latest"
)
c.DockerSpawner.network_name = os.environ.get("DOCKER_NETWORK", "hub-network")
c.DockerSpawner.remove = True
c.DockerSpawner.use_internal_ip = True
c.JupyterHub.hub_connect_ip = os.environ.get("HUB_CONNECT_IP", "")

# Allow spawned containers to reach host services (Hugr, Keycloak in dev)
c.DockerSpawner.extra_host_config = {
    "extra_hosts": {"host.docker.internal": "host-gateway"},
}

# Persistent user data
c.DockerSpawner.volumes = {
    "hub-user-{username}": "/home/jovyan/work",
}

# ===========================================================================
# Token injection (only access_token, never refresh_token)
# ===========================================================================

HUGR_CONNECTION_NAME = os.environ.get("HUGR_CONNECTION_NAME", "default")


async def pre_spawn_hook(spawner, auth_state):
    """Pass Hugr URL and initial access token to workspace container.

    NEVER passes refresh_token, client_secret, or OIDC endpoints.
    """
    spawner.environment["HUGR_URL"] = HUGR_URL
    spawner.environment["HUGR_CONNECTION_NAME"] = HUGR_CONNECTION_NAME

    if auth_state:
        access_token = auth_state.get("access_token", "")
        if access_token:
            spawner.environment["HUGR_INITIAL_ACCESS_TOKEN"] = access_token


c.Spawner.auth_state_hook = pre_spawn_hook

# ===========================================================================
# Roles (server can read auth_state for token polling)
# ===========================================================================

c.JupyterHub.load_roles = [
    {
        "name": "user",
        "scopes": ["self", "admin:auth_state!user"],
    },
    {
        "name": "server",
        "scopes": [
            "users:activity!user",
            "access:servers!server",
            "admin:auth_state!user",
        ],
    },
]

# ===========================================================================
# Hub Service notification (optional, for Stage 2+)
# ===========================================================================

HUB_SERVICE_URL = os.environ.get("HUB_SERVICE_URL")

if HUB_SERVICE_URL:

    async def post_auth_hook(authenticator, handler, authentication):
        auth_state = authentication.get("auth_state", {})
        async with httpx.AsyncClient() as client:
            try:
                await client.post(
                    f"{HUB_SERVICE_URL}/api/user/login",
                    json={
                        "user_id": authentication["name"],
                        "user_name": auth_state.get(
                            "name", authentication["name"]
                        ),
                        "role": auth_state.get("x-hugr-role", ""),
                        "email": auth_state.get("email", ""),
                    },
                    timeout=5,
                )
            except Exception as e:
                logger.warning("Failed to notify Hub Service: %s", e)
        return authentication

    c.Authenticator.post_auth_hook = post_auth_hook
