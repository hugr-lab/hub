"""
JupyterHub configuration for Analytics Hub.

OIDC endpoints are auto-discovered from Hugr's /auth/config endpoint.
Only three env vars required: HUGR_URL, OIDC_CLIENT_SECRET, HUB_BASE_URL.
"""

import base64
import json
import logging
import os
import time

import httpx
from oauthenticator.generic import GenericOAuthenticator
from tornado import web
from tornado.httpclient import HTTPClientError

SPAWNER_TYPE = os.environ.get("HUGR_SPAWNER", "docker")

logger = logging.getLogger(__name__)

# K8s: load env vars from ConfigMap mounted at /opt/hub/env/
# (z2jh doesn't support envFrom, so we load from files)
_env_dir = "/opt/hub/env"
if os.path.isdir(_env_dir):
    for name in os.listdir(_env_dir):
        if name.startswith(".") or name.startswith(".."):
            continue
        path = os.path.join(_env_dir, name)
        if os.path.isfile(path):
            with open(path) as f:
                val = f.read().strip()
                if val and name not in os.environ:
                    os.environ[name] = val

# ===========================================================================
# Required configuration
# ===========================================================================

HUGR_URL = os.environ["HUGR_URL"]  # e.g. http://hugr:15000
HUB_BASE_URL = os.environ["HUB_BASE_URL"]  # e.g. http://localhost:8000
HUGR_TLS_SKIP_VERIFY = os.environ.get("HUGR_TLS_SKIP_VERIFY", "false").lower() == "true"
OIDC_TLS_SKIP_VERIFY = os.environ.get("OIDC_TLS_SKIP_VERIFY", "false").lower() == "true"
_hugr_tls_verify = not HUGR_TLS_SKIP_VERIFY
_oidc_tls_verify = not OIDC_TLS_SKIP_VERIFY

# Hub TLS (optional — for direct HTTPS without reverse proxy)
_hub_ssl_cert = os.environ.get("HUB_SSL_CERT", "")
_hub_ssl_key = os.environ.get("HUB_SSL_KEY", "")
if _hub_ssl_cert and _hub_ssl_key:
    c.JupyterHub.ssl_cert = _hub_ssl_cert
    c.JupyterHub.ssl_key = _hub_ssl_key

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
            resp = httpx.get(f"{HUGR_URL}/auth/config", timeout=10, verify=_hugr_tls_verify)
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
                f"{issuer}/.well-known/openid-configuration", timeout=10,
                verify=_oidc_tls_verify,
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
# Proactive token refresh hook
# ===========================================================================
# OAuthenticator's stock refresh_user is *lazy*: it only triggers a real
# Keycloak refresh after the current access_token has died (userinfo lookup
# returns 401 → fallback to refresh_token branch). That leaves a "death
# window" between exp and the next refresh where kernel requests fail with
# 401, and — worse — refresh only happens at all when something hits JH with
# user-cookie auth. The workspace's hugr_connection_service poller hits JH
# with a server token (JUPYTERHUB_API_TOKEN), which still goes through
# get_current_user → refresh_auth → refresh_user → this hook.
#
# Wiring: just `c.GenericOAuthenticator.refresh_user_hook = ...` plus a low
# `auth_refresh_age` so refresh_auth doesn't dedupe across polls. No custom
# Authenticator subclass, no custom routes.

# Default refresh margin in seconds — the hook does a real KC refresh as
# soon as the current access_token is closer than this margin to its exp.
# Configurable via HUGR_TOKEN_REFRESH_MARGIN_SECONDS env (minimum 5).
_REFRESH_MARGIN_SECONDS_DEFAULT = 30


def _decode_jwt_exp(token):
    """Decode the `exp` claim from a JWT without signature verification."""
    if not token:
        return None
    try:
        parts = token.split(".")
        if len(parts) < 2:
            return None
        payload = parts[1] + "=" * (4 - len(parts[1]) % 4)
        claims = json.loads(base64.urlsafe_b64decode(payload))
        exp = claims.get("exp")
        return float(exp) if exp is not None else None
    except Exception:
        return None


async def _proactive_refresh_hook(authenticator, user, auth_state):
    """Proactive token refresh hook for GenericOAuthenticator.refresh_user_hook.

    The default oauthenticator `refresh_user` flow is *lazy*: it only triggers
    a real Keycloak refresh after the current access_token has actually died
    (userinfo lookup returns 401 → fallback to refresh_token branch). That
    creates a "death window" between exp and the next refresh where kernel
    requests fail with 401.

    This hook makes refresh *proactive*: as soon as the current access_token
    is within `HUGR_TOKEN_REFRESH_MARGIN_SECONDS` of its exp, we directly
    POST to Keycloak's /token endpoint with the refresh_token and return a
    fresh auth_model. JupyterHub persists it via auth_to_user automatically.

    Returns:
      True       — current access_token is still safely valid; no refresh
      False      — refresh_token is missing/invalid → fresh login required
      auth_model — successful KC refresh, JH will save and use this
      None       — transient failure (network, 5xx); fall back to default
                   lazy refresh_user flow
    """
    if not auth_state:
        return False
    refresh_token = auth_state.get("refresh_token")
    if not refresh_token:
        return False

    margin = _REFRESH_MARGIN_SECONDS_DEFAULT
    try:
        margin = max(int(os.environ.get("HUGR_TOKEN_REFRESH_MARGIN_SECONDS", margin)), 5)
    except ValueError:
        pass

    # If current token still has plenty of life, skip the refresh.
    exp = _decode_jwt_exp(auth_state.get("access_token", ""))
    if exp is not None and exp - time.time() > margin:
        return True

    # Within margin (or already dead) — actively exchange refresh_token at KC.
    # handler=None: get_token_info goes through self.httpfetch and never reads
    # the handler arg for the token endpoint call (verified in oauthenticator
    # 17.4.0). If a future version starts using it, this will surface as a
    # clean AttributeError instead of a silent miss.
    try:
        params = authenticator.build_refresh_token_request_params(refresh_token)
        token_info = await authenticator.get_token_info(None, params)
    except HTTPClientError as e:
        if 400 <= e.code < 500:
            authenticator.log.info(
                "refresh_user_hook: invalid_grant for %s (HTTP %d). Requiring fresh login.",
                user.name, e.code,
            )
            return False
        authenticator.log.warning(
            "refresh_user_hook: KC token endpoint returned %d for %s, falling back to lazy flow.",
            e.code, user.name,
        )
        return None
    except web.HTTPError as e:
        # get_token_info raises HTTPError(403) on error_description responses
        # (e.g. invalid_grant body without HTTP 4xx)
        if 400 <= e.status_code < 500:
            authenticator.log.info(
                "refresh_user_hook: bad token response for %s (%s). Requiring fresh login.",
                user.name, e.log_message,
            )
            return False
        return None
    except Exception as e:
        authenticator.log.warning(
            "refresh_user_hook: unexpected error refreshing %s: %s. Falling back.",
            user.name, e,
        )
        return None

    # KC may not return a new refresh_token (depends on rotation policy).
    # Preserve the existing one in that case so we don't lose the ability
    # to refresh next time.
    if not token_info.get("refresh_token"):
        token_info["refresh_token"] = refresh_token

    try:
        auth_model = await authenticator._token_to_auth_model(token_info)
    except Exception as e:
        authenticator.log.warning(
            "refresh_user_hook: failed to build auth_model for %s: %s",
            user.name, e,
        )
        return None
    # Returning auth_model — JH's BaseHandler.refresh_auth will persist it via
    # auth_to_user(auth_model, user) automatically. We don't (and shouldn't)
    # call user.save_auth_state ourselves; doing so here would race with JH's
    # own save and could leak partial state.
    return auth_model


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

# OIDC role extraction + hub:management.admin capability check via post_auth_hook.
#
# Admin detection is driven entirely by Hugr core.role_permissions — deployments
# grant the hub:management.admin capability to whatever role they consider
# administrative, and both JupyterHub (this file) and hub-service consult the
# same table. No role name is hard-coded here.

HUB_SERVICE_URL = os.environ.get("HUB_SERVICE_URL")


async def _has_hub_management_admin(user_id: str, user_name: str, role: str) -> bool:
    """Return True if the caller's role has the hub:management.admin capability
    enabled in Hugr core.role_permissions.

    Uses function.core.auth.check_access with the management secret +
    x-hugr-impersonated-* headers to evaluate the permission against the
    target role (requires the admin-keyed role in Hugr to have
    can_impersonate=true — see .env.example for setup).
    """
    hugr_secret = os.environ.get("HUGR_SECRET_KEY", "")
    if not hugr_secret or not role:
        return False
    try:
        async with httpx.AsyncClient(verify=_hugr_tls_verify) as client:
            resp = await client.post(
                f"{HUGR_URL}/query",
                json={"query": (
                    '{ function { core { auth { check_access('
                    'type_name: "hub:management", fields: "admin"'
                    ') { field enabled } } } } }'
                )},
                headers={
                    "X-Hugr-Secret-Key": hugr_secret,
                    "x-hugr-impersonated-user-id": user_id,
                    "x-hugr-impersonated-user-name": user_name or user_id,
                    "x-hugr-impersonated-role": role,
                },
                timeout=5,
            )
        entries = (
            (resp.json() or {})
            .get("data", {})
            .get("function", {})
            .get("core", {})
            .get("auth", {})
            .get("check_access")
            or []
        )
        for e in entries:
            if e.get("field") == "admin":
                return e.get("enabled", False)
    except Exception as e:
        logger.warning("Failed to check hub:management.admin capability for %s: %s", user_id, e)
    return False


async def _post_auth_hook(authenticator, handler, authentication):
    """Extract OIDC roles → JupyterHub groups + admin flag (via capability) + sync user."""
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

    # Resolve the user_id in the same shape Hugr will use via `auth.me`
    # (= the OIDC `sub` claim by default). hub.db.users is keyed by this
    # id so user_agents / conversations / agent_memory FKs line up with
    # whatever the Go airport-go handlers receive via ArgFromContext.
    oauth_user = auth_state.get("oauth_user", {}) or {}
    user_sub = oauth_user.get("sub") or auth_state.get("sub") or authentication["name"]
    display_name = (
        oauth_user.get("name")
        or oauth_user.get("preferred_username")
        or auth_state.get("name")
        or authentication["name"]
    )

    # Admin flag — evaluated via the hub:management.admin capability in Hugr
    # core.role_permissions. No role names are hard-coded; whatever roles the
    # deployment has granted the capability to become admins automatically.
    primary_role = roles[0] if roles else ""
    if await _has_hub_management_admin(user_sub, display_name, primary_role):
        authentication["admin"] = True

    # Upsert user into hub.db.users via Hugr GraphQL (management secret auth).
    # This previously called Hub Service's /api/user/login, which has been
    # removed — Hugr is now the single write path for all hub.db.* data.
    hugr_secret = os.environ.get("HUGR_SECRET_KEY", "")
    if hugr_secret:
        async with httpx.AsyncClient(verify=_hugr_tls_verify) as client:
            try:
                user_id = user_sub
                user_name = display_name
                role = roles[0] if roles else "user"
                email = oauth_user.get("email") or auth_state.get("email") or ""
                # One round-trip: try an update, fall back to insert if zero affected.
                gql = (
                    "mutation($id:String!,$name:String!,$role:String!,$email:String) {"
                    " hub { db {"
                    "  update_users(filter:{id:{eq:$id}}, data:{display_name:$name, hugr_role:$role, email:$email}) { affected_rows }"
                    " } }"
                    "}"
                )
                resp = await client.post(
                    f"{HUGR_URL}/query",
                    json={
                        "query": gql,
                        "variables": {"id": user_id, "name": user_name, "role": role, "email": email},
                    },
                    headers={"X-Hugr-Secret-Key": hugr_secret},
                    timeout=5,
                )
                affected = 0
                try:
                    affected = int(
                        (resp.json() or {}).get("data", {}).get("hub", {}).get("db", {}).get("update_users", {}).get("affected_rows", 0)
                    )
                except Exception:
                    affected = 0
                if affected == 0:
                    ins = (
                        "mutation($id:String!,$name:String!,$role:String!,$email:String) {"
                        " hub { db { insert_users(data:{id:$id, display_name:$name, hugr_role:$role, email:$email}) { id } } }"
                        "}"
                    )
                    await client.post(
                        f"{HUGR_URL}/query",
                        json={
                            "query": ins,
                            "variables": {"id": user_id, "name": user_name, "role": role, "email": email},
                        },
                        headers={"X-Hugr-Secret-Key": hugr_secret},
                        timeout=5,
                    )
            except Exception as e:
                logger.warning("Failed to upsert user in Hugr: %s", e)

    return authentication

c.GenericOAuthenticator.post_auth_hook = _post_auth_hook
c.GenericOAuthenticator.refresh_user_hook = _proactive_refresh_hook
c.GenericOAuthenticator.manage_groups = True

# Override default auth_state_groups_key ("oauth_user.groups") which doesn't exist
# in Keycloak userinfo and causes groups to be wiped on refresh_pre_spawn.
# This callable extracts groups from our custom OIDC claims — same logic as post_auth_hook.
def _groups_from_auth_state(auth_state):
    from hub_profiles.resolver import extract_claim
    groups = []
    _pc = os.environ.get("HUGR_PROFILE_CLAIM", "")
    if _pc:
        for v in extract_claim(auth_state, _pc):
            groups.append(f"profile:{v}")
    for r in extract_claim(auth_state, os.environ.get("HUGR_ROLE_CLAIM", "x-hugr-role")):
        groups.append(f"role:{r}")
    return groups

c.GenericOAuthenticator.auth_state_groups_key = _groups_from_auth_state

c.GenericOAuthenticator.enable_auth_state = True
c.GenericOAuthenticator.refresh_pre_spawn = True
# Low refresh age — we want refresh_user_hook to be called on (almost) every
# authenticated request from the workspace's hub_token_provider poller, not
# deduped by JH. The hook itself decides whether a real KC refresh is needed
# based on the access_token's exp vs HUGR_TOKEN_REFRESH_MARGIN_SECONDS.
c.GenericOAuthenticator.auth_refresh_age = 10
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

if SPAWNER_TYPE == "kubernetes":
    from kubespawner import KubeSpawner

    c.JupyterHub.spawner_class = KubeSpawner

    c.KubeSpawner.image = os.environ.get(
        "SINGLEUSER_IMAGE", "hugr-lab/hub-singleuser:latest"
    )

    # PVC for user home directory
    c.KubeSpawner.storage_pvc_ensure = True
    _storage_class = os.environ.get("STORAGE_CLASS", "")
    if _storage_class:
        c.KubeSpawner.storage_class = _storage_class
    c.KubeSpawner.storage_capacity = os.environ.get("STORAGE_CAPACITY", "10Gi")
    c.KubeSpawner.storage_access_modes = ["ReadWriteOnce"]

    # FUSE mounts require privileged container (s3fs, blobfuse2)
    # Entrypoint runs as root for FUSE, then gosu drops to jovyan
    _fuse_enabled = os.environ.get("HUGR_FUSE_ENABLED", "false").lower() == "true"
    if _fuse_enabled:
        c.KubeSpawner.extra_container_config = {
            "securityContext": {
                "privileged": True,
                "capabilities": {"add": ["SYS_ADMIN"]},
            }
        }

    # Profile list from profiles.json
    from hub_profiles import load_profiles
    from hub_profiles.spawner import build_k8s_profiles
    _k8s_config = load_profiles()
    c.KubeSpawner.profile_list = build_k8s_profiles(_k8s_config)

else:
    from dockerspawner import DockerSpawner

    c.JupyterHub.spawner_class = DockerSpawner

    c.DockerSpawner.image = os.environ.get(
        "SINGLEUSER_IMAGE", "hugr-lab/hub-singleuser:latest"
    )
    c.DockerSpawner.network_name = os.environ.get("DOCKER_NETWORK", "hub-network")
    c.DockerSpawner.remove = True
    c.DockerSpawner.use_internal_ip = True
    c.JupyterHub.hub_connect_ip = os.environ.get("HUB_CONNECT_IP", "")

    # Allow spawned containers to reach host services + FUSE for storage mounts
    c.DockerSpawner.extra_host_config = {
        "extra_hosts": {"host.docker.internal": "host-gateway"},
        "cap_add": ["SYS_ADMIN"],
        "devices": ["/dev/fuse:/dev/fuse:rwm"],
        "security_opt": ["apparmor:unconfined"],
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

    # Single match — no profile selection, but still capture timezone
    if len(matched) <= 1:
        return (
            '<input type="hidden" name="timezone" id="user-timezone">'
            '<script>document.getElementById("user-timezone").value='
            'Intl.DateTimeFormat().resolvedOptions().timeZone;</script>'
        )

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
    html += '<input type="hidden" name="timezone" id="user-timezone">'
    html += '<script>document.getElementById("user-timezone").value=Intl.DateTimeFormat().resolvedOptions().timeZone;</script>'
    html += '</div>'
    return html

c.Spawner.options_form = _options_form

def _options_from_form(formdata):
    return {
        "profile": formdata.get("profile", [""])[0],
        "timezone": formdata.get("timezone", [""])[0],
    }

c.Spawner.options_from_form = _options_from_form

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
    # Merge with base volumes (Docker only — K8s uses PVC via KubeSpawner)
    if SPAWNER_TYPE != "kubernetes":
        storage_path = os.environ.get("HUB_STORAGE_PATH", "/var/hub-storage")
        # JupyterHub username is the stable short handle — keep using it for
        # the user's own home directory path so it stays predictable across
        # OIDC provider changes.
        user_name = spawner.user.name
        # The Hugr-side user id (what hub.db.users.id and all FKs are keyed
        # by) is the OIDC `sub` claim — same as what _post_auth_hook stored.
        oauth_user = (auth_state or {}).get("oauth_user", {}) or {}
        hugr_user_id = oauth_user.get("sub") or (auth_state or {}).get("sub") or user_name
        base_volumes = {
            os.path.join(storage_path, "users", user_name): "/home/jovyan/work",
        }
        # Mount shared agent directories. Hugr is queried directly via the
        # management secret + impersonation header, replacing the removed
        # Hub Service /api/user/agents REST endpoint.
        hugr_secret = os.environ.get("HUGR_SECRET_KEY", "")
        if hugr_secret:
            try:
                import httpx
                gql = (
                    "query($uid:String!) { hub { db { user_agents("
                    " filter:{user_id:{eq:$uid}}"
                    ") { agent { id display_name } } } } }"
                )
                resp = httpx.post(
                    f"{HUGR_URL}/query",
                    json={"query": gql, "variables": {"uid": hugr_user_id}},
                    headers={
                        "X-Hugr-Secret-Key": hugr_secret,
                        "x-hugr-impersonated-user-id": hugr_user_id,
                        "x-hugr-impersonated-user-name": user_name,
                        "x-hugr-impersonated-role": "admin",
                    },
                    timeout=5,
                    verify=_hugr_tls_verify,
                )
                if resp.status_code == 200:
                    payload = (resp.json() or {}).get("data", {}).get("hub", {}).get("db", {}).get("user_agents", []) or []
                    for ua in payload:
                        agent = ua.get("agent") or {}
                        agent_id = agent.get("id", "")
                        agent_name = agent.get("display_name", agent_id)
                        if agent_id:
                            host_path = os.path.join(storage_path, "shared", agent_id)
                            container_path = f"/home/jovyan/agents/{agent_name}"
                            base_volumes[host_path] = container_path
            except Exception as e:
                spawner.log.warning(f"Failed to fetch user agents for volume mounts: {e}")
        base_volumes.update(storage_volumes)
        spawner.volumes = base_volumes

    # 6. Pass user timezone (from browser via options_form)
    user_tz = spawner.user_options.get("timezone", "")
    if user_tz:
        spawner.environment["TZ"] = user_tz

    # 7. Admin flag for hub-admin extension. JupyterHub's user.admin is set in
    #    post_auth_hook from the user's hub:management.admin capability, so
    #    here we just forward it as an env var.
    if spawner.user.admin:
        spawner.environment["HUGR_HUB_ADMIN"] = "true"

    # 8. Pass Hub Service + Hugr connection
    if HUB_SERVICE_URL:
        spawner.environment["HUB_SERVICE_URL"] = HUB_SERVICE_URL
    spawner.environment["HUGR_URL"] = HUGR_URL
    spawner.environment["HUGR_CONNECTION_NAME"] = HUGR_CONNECTION_NAME
    if HUGR_TLS_SKIP_VERIFY:
        spawner.environment["HUGR_TLS_SKIP_VERIFY"] = "true"
    _query_timeout = os.environ.get("HUGR_QUERY_TIMEOUT")
    if _query_timeout:
        spawner.environment["HUGR_QUERY_TIMEOUT"] = _query_timeout
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
            sys.executable, "/opt/hub/scripts/idle-culler-with-notify.py",
        ],
        "environment": {
            "HUGR_IDLE_TIMEOUT": str(_idle_timeout),
            "HUGR_MAX_SERVER_AGE": str(_max_age),
            "HUGR_CULL_INTERVAL": str(_cull_interval),
            "HUGR_CULL_ADMINS": "true" if _cull_admins else "false",
            "HUGR_CULL_WARN_BEFORE": os.environ.get("HUGR_CULL_WARN_BEFORE", "300"),
        },
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



