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
                "end_session_url": oidc_config.get("end_session_endpoint", ""),
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

# Admin users — from env (static) + OIDC claim (dynamic)
_admin_users = os.environ.get("HUGR_ADMIN_USERS", "")
if _admin_users:
    c.Authenticator.admin_users = set(_admin_users.split(","))

# OIDC role/admin extraction via post_auth_hook
_admin_claim = os.environ.get("HUGR_ADMIN_CLAIM", "")
_admin_values = set(os.environ.get("HUGR_ADMIN_VALUES", "admin").split(","))

async def _post_auth_hook(authenticator, handler, authentication):
    """Extract OIDC roles → JupyterHub groups + admin flag."""
    from hub_profiles.resolver import extract_claim

    auth_state = authentication.get("auth_state", {})
    groups = []

    # Profile claim → groups (for profile resolution in pre_spawn_hook)
    profile_claim = os.environ.get("HUGR_PROFILE_CLAIM", "")
    if profile_claim:
        values = extract_claim(auth_state, profile_claim)
        for v in values:
            groups.append(f"profile:{v}")

    # Role claim → groups
    role_claim = os.environ.get("HUGR_ROLE_CLAIM", "x-hugr-role")
    roles = extract_claim(auth_state, role_claim)
    for r in roles:
        groups.append(f"role:{r}")

    if groups:
        authentication["groups"] = groups

    # Admin flag from claim
    if _admin_claim:
        admin_values = extract_claim(auth_state, _admin_claim)
        if any(v in _admin_values for v in admin_values):
            authentication["admin"] = True
    elif roles:
        # Fallback: check roles against admin values
        if any(r in _admin_values for r in roles):
            authentication["admin"] = True

    return authentication

c.GenericOAuthenticator.post_auth_hook = _post_auth_hook
c.GenericOAuthenticator.manage_groups = True

c.GenericOAuthenticator.enable_auth_state = True
c.GenericOAuthenticator.refresh_pre_spawn = True
c.GenericOAuthenticator.auth_refresh_age = 120
c.GenericOAuthenticator.username_claim = os.environ.get(
    "OIDC_USERNAME_CLAIM", "preferred_username"
)
c.GenericOAuthenticator.allow_all = True

# OIDC logout — redirect to IdP end_session_endpoint
if oidc.get("end_session_url"):
    _logout_url = oidc["end_session_url"]
    _post_logout_redirect = f"{HUB_BASE_URL}/hub/login"
    c.GenericOAuthenticator.logout_redirect_url = (
        f"{_logout_url}?client_id={oidc['client_id']}"
        f"&post_logout_redirect_uri={_post_logout_redirect}"
    )

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

# ---------------------------------------------------------------------------
# Profile selection UI (when user has multiple profile matches)
# ---------------------------------------------------------------------------

def _options_form(spawner):
    """Build profile selection form from profiles.json + user's claims."""
    from hub_profiles import load_profiles
    from hub_profiles.resolver import extract_claim, _match_profiles

    config = load_profiles()
    profiles = config.get("profiles", {})

    # Determine matched profiles from user groups (set by post_auth_hook)
    user_groups = {g.name for g in spawner.user.groups}
    profile_claim = os.environ.get("HUGR_PROFILE_CLAIM", "")

    matched = []
    if profile_claim:
        # Extract claim values from groups (post_auth_hook stores as "profile:{value}")
        claim_values = [g.replace("profile:", "") for g in user_groups if g.startswith("profile:")]
        if claim_values:
            matched = _match_profiles(claim_values, profiles)

    # Fallback: role_map
    if not matched:
        role_values = [g.replace("role:", "") for g in user_groups if g.startswith("role:")]
        role_map = config.get("role_map", {})
        for role in role_values:
            if role in role_map and role_map[role] in profiles:
                matched = [(role_map[role], profiles[role_map[role]])]
                break

    # Fallback: default
    if not matched:
        default = config.get("default_profile", "default")
        if default in profiles:
            matched = [(default, profiles[default])]

    # Single match — no UI needed
    if len(matched) <= 1:
        return ""

    # Multiple matches — show selection
    matched.sort(key=lambda x: x[1].get("rank", 0))
    html = '<div style="padding:16px;max-width:500px"><h3 style="margin:0 0 12px">Select workspace size</h3>'
    for i, (name, p) in enumerate(matched):
        checked = "checked" if i == len(matched) - 1 else ""  # highest rank default
        desc = p.get("description", "")
        mem = p.get("mem_limit", "unlimited")
        cpu = p.get("cpu_limit", "unlimited")
        html += f'''
        <label style="display:block;padding:10px 12px;margin:6px 0;border:1px solid #ddd;border-radius:6px;cursor:pointer">
            <input type="radio" name="profile" value="{name}" {checked} style="margin-right:8px">
            <strong>{p.get("display_name", name)}</strong>
            <span style="color:#666;margin-left:8px">{mem} RAM, {cpu} CPU</span>
            {f'<br><span style="color:#888;font-size:0.85em;margin-left:24px">{desc}</span>' if desc else ''}
        </label>'''
    html += '</div>'
    return html

c.DockerSpawner.options_form = _options_form

def _options_from_form(formdata):
    return {"profile": formdata.get("profile", [""])[0]}

c.DockerSpawner.options_from_form = _options_from_form

# Persistent user data — base volume set here, additional mounts from profiles.json in pre_spawn_hook

# ===========================================================================
# Token injection (only access_token, never refresh_token)
# ===========================================================================

HUGR_CONNECTION_NAME = os.environ.get("HUGR_CONNECTION_NAME", "default")


async def pre_spawn_hook(spawner, auth_state):
    """Configure workspace: resource limits, storage, Hugr connection.

    NEVER passes refresh_token, client_secret, or OIDC endpoints.
    """
    from hub_profiles import load_profiles, resolve_profile, check_budgets, apply_profile, build_volumes

    # 1. Load profiles (re-reads file every spawn, no restart needed)
    config = load_profiles()

    # 2. Resolve profile from OIDC claims
    matched = resolve_profile(spawner, auth_state or {}, config)
    if matched:
        # If user selected a profile (from options_form)
        user_choice = spawner.user_options.get("profile")
        if user_choice:
            profile = next((p for n, p in matched if n == user_choice), matched[0][1])
            profile_name = user_choice
        else:
            profile_name, profile = matched[0]
    else:
        default = config.get("default_profile", "default")
        profile = config.get("profiles", {}).get(default, {})
        profile_name = default

    # 3. Check budgets
    check_budgets(spawner, profile, config)

    # 4. Apply resource limits
    apply_profile(spawner, profile, profile_name)

    # 5. Build storage volumes
    access_token = (auth_state or {}).get("access_token", "")
    storage_volumes = build_volumes(
        spawner, profile, config,
        auth_state=auth_state,
        access_token=access_token,
    )
    # Merge with base volumes (user workspace)
    base_volumes = {"hub-user-{username}": "/home/jovyan/work"}
    base_volumes.update(storage_volumes)
    spawner.volumes = base_volumes

    # 6. Pass Hugr connection
    spawner.environment["HUGR_URL"] = HUGR_URL
    spawner.environment["HUGR_CONNECTION_NAME"] = HUGR_CONNECTION_NAME
    if access_token:
        spawner.environment["HUGR_INITIAL_ACCESS_TOKEN"] = access_token


c.Spawner.auth_state_hook = pre_spawn_hook

# ===========================================================================
# Prometheus metrics (allow unauthenticated scraping for monitoring)
# ===========================================================================

c.JupyterHub.authenticate_prometheus = False

# ===========================================================================
# Idle Culler — auto-stop idle workspaces (per-profile timeouts)
# ===========================================================================

import sys

_idle_timeout = int(os.environ.get("HUGR_IDLE_TIMEOUT", "3600"))
_max_age = int(os.environ.get("HUGR_MAX_SERVER_AGE", "86400"))
_cull_interval = int(os.environ.get("HUGR_CULL_INTERVAL", "300"))
_cull_admins = os.environ.get("HUGR_CULL_ADMINS", "false").lower() == "true"

c.JupyterHub.services = [
    {
        "name": "idle-culler",
        "command": [
            sys.executable, "-m", "jupyterhub_idle_culler",
            f"--timeout={_idle_timeout}",
            f"--max-age={_max_age}",
            f"--cull-every={_cull_interval}",
        ] + (["--cull-admin-users"] if _cull_admins else []),
    }
]

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
    {
        "name": "idle-culler",
        "scopes": [
            "list:users",
            "read:users:activity",
            "read:servers",
            "delete:servers",
        ],
        "services": ["idle-culler"],
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
