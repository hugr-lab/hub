# Stage 1 Specification: JupyterHub + OIDC + User Workspace

## Goal

Users log in via OIDC, get an isolated Docker container with JupyterLab and three pre-configured kernels (Hugr GraphQL, DuckDB, Python). The Hugr connection is fully managed by the Hub — tokens refresh automatically, the user cannot delete or modify the managed connection. No Hub Service required at this stage.

## Components

```text
┌──────────────┐  ┌───────────────┐  ┌──────────────┐
│ JupyterHub   │  │ Hugr Server   │  │ Keycloak     │
│              │  │               │  │ (OIDC)       │
│ OAuthenti-   │  │ OIDC token    │  │              │
│ cator        │  │ validation    │  │ hugr-hub     │
│ DockerSpawner│  │ RBAC          │  │ (confidential│
│              │  │               │  │  client)     │
└──────┬───────┘  └───────────────┘  └──────────────┘
       │
  ┌────▼──────────────────────────────────────────┐
  │ User Workspace Container                       │
  │                                                │
  │ JupyterLab                                     │
  │ ├── Hugr Kernel (GraphQL)                      │
  │ ├── DuckDB Kernel (SQL)                        │
  │ ├── Python Kernel (hugr-client)                │
  │ └── hugr_connection_service                    │
  │     ├── Managed connection (hub, read-only)    │
  │     └── Token refresh via JupyterHub API       │
  └────────────────────────────────────────────────┘
```

## Token Refresh Architecture

Refresh token **never enters the container**. JupyterHub manages OIDC refresh. Container pulls fresh access_token from JupyterHub API.

```text
JupyterHub                                  Workspace Container
    │                                            │
    │  auth_state (encrypted in Hub DB):         │
    │  {access_token, refresh_token, exp}        │
    │                                            │
    │  refresh_user() runs every N seconds       │
    │  (triggered by auth_refresh_age setting)    │
    │  → POST token_endpoint                     │
    │    grant_type=refresh_token                 │
    │  → updates auth_state in DB                │
    │                                            │
    │          GET /hub/api/user                  │
    │          Authorization: Bearer              │
    │            $JUPYTERHUB_API_TOKEN            │
    │<───────────────────────────────────────────│
    │                                            │
    │  {auth_state: {access_token: "eyJ..."}}    │
    │───────────────────────────────────────────>│
    │                                            │
    │                                            │  Parse exp from JWT
    │                                            │  Schedule next refresh
    │                                            │  at exp - 30 seconds
    │                                            │
    │                                            │  Write access_token to
    │                                            │  connections.json
    │                                            │
    │                                            │  Kernels read fresh
    │                                            │  token on next query
```

### Refresh timing

- Connection service decodes `exp` claim from the access_token JWT (base64 decode the payload, no signature verification needed — just reading the expiry).
- Schedules next refresh at `exp - 30 seconds`.
- If fetch fails, retries with exponential backoff (5s, 10s, 20s, 40s, max 60s).
- If access_token from Hub API is the same (not yet refreshed by Hub), retry in 10 seconds.

### What enters the container

| Variable | Source | Sensitivity | Purpose |
| -------- | ------ | ----------- | ------- |
| `JUPYTERHUB_API_TOKEN` | JupyterHub (standard) | Medium — scoped to this user's server | Poll Hub API for fresh access_token |
| `JUPYTERHUB_API_URL` | JupyterHub (standard) | Low | Hub API endpoint |
| `HUGR_URL` | pre_spawn_hook | Low | Hugr server URL (for managed connection) |
| `HUGR_CONNECTION_NAME` | pre_spawn_hook | Low | Name for managed connection (default: "default") |
| `HUGR_INITIAL_ACCESS_TOKEN` | pre_spawn_hook | Low — short-lived, based on token exp | Bootstrap: first query works before poll kicks in |

**NOT in container**: refresh_token, client_secret, OIDC client_id, OIDC endpoints, management secret key.

## Managed Connection Behavior

### Connection Types

```text
Managed (created by Hub):
  - name: from HUGR_CONNECTION_NAME env or "default"
  - url: from HUGR_URL env
  - auth_type: "hub"  (new type)
  - managed: true
  - Token refreshed by HubTokenProvider
  - User CANNOT: delete, change URL, change auth_type, change name
  - User CAN: see connection status, see token expiry, run :whoami

User-created (as before):
  - Any name, url, auth_type
  - managed: false (default)
  - User has full control
  - Cannot shadow managed connection name
```

### UI Behavior

Connection Manager panel in JupyterLab:

```text
┌─────────────────────────────────────────┐
│ Connections                              │
│                                         │
│ 🔒 default (hub-managed)               │
│    https://hugr.example.com/ipc         │
│    Status: authenticated                │
│    Expires: 12:45:30 (in 4m)           │
│    User: alice (analyst)                │
│    [Test] [Whoami]                      │
│                                         │
│ ── User connections ──                  │
│                                         │
│ ✏️  my-local                            │
│    http://localhost:15004/ipc           │
│    auth: public                         │
│    [Edit] [Delete] [Test]              │
│                                         │
│ [+ Add Connection]                      │
└─────────────────────────────────────────┘
```

### Go Kernel Behavior

- On startup: reads connections.json, finds managed connection, sets as default
- Before each query: checks `expires_at` from connections.json
  - If > 30s remaining: use cached token
  - If < 30s or expired: re-read connections.json (connection_service may have updated it)
  - If still expired after re-read: return error "Token expired, waiting for refresh..."
- Meta commands:
  - `:connections` — shows managed connections with `[managed]` label
  - `:use default` — allowed (switching TO managed)
  - `:auth`, `:key`, `:token` on managed connection — rejected: "Cannot modify hub-managed connection"
  - `:connect` with same name as managed — rejected: "Name 'default' is reserved for hub-managed connection"

### Python Kernel Behavior

```python
from hugr import HugrClient

# Reads HUGR_URL from env, token from connections.json
client = HugrClient()  # works immediately
result = client.query("{ core { info { version } } }")
```

hugr-client reads token from `connections.json` (already supported via `HUGR_CONFIG_PATH`). Alternatively reads `HUGR_TOKEN` env — but this is static. For long sessions, reading from connections.json is better because it gets refreshed.

**Change needed in hugr-client**: add option to read token from connections.json by connection name (currently only reads from env vars).

## Changes Required

### 1. This repo (hub): new files

| File | Description |
| ---- | ----------- |
| `Dockerfile.hub` | JupyterHub image with oauthenticator, dockerspawner, httpx |
| `Dockerfile.singleuser` | Multi-stage build: JupyterLab + Go kernels + extensions |
| `jupyterhub_config.py` | OAuthenticator + DockerSpawner + pre_spawn_hook |
| `docker-compose.yml` | Hub + Hugr + Keycloak for dev with OIDC |
| `docker-compose.local.yml` | JupyterLab + Hugr for local dev without OIDC |
| `.env.example` | Template for all config variables |

### 2. hugr-kernel: connection_service changes

#### 2.1 New file: `hugr_connection_service/hub_token_provider.py`

Token refresh via JupyterHub API. Core logic:

```python
class HubTokenProvider:
    """Refreshes access_token by polling JupyterHub API.
    No refresh_token in container — Hub manages OIDC refresh."""

    def __init__(self, connection_name: str, hugr_url: str,
                 initial_access_token: str | None = None):
        self.connection_name = connection_name
        self.hugr_url = hugr_url
        self.hub_api_url = os.environ.get("JUPYTERHUB_API_URL")
        self.hub_token = os.environ.get("JUPYTERHUB_API_TOKEN")
        self._refresh_handle = None

        if initial_access_token:
            self._write_token(initial_access_token)

    def start(self):
        """Begin polling loop. Schedule based on token exp."""
        if not self.hub_api_url:
            return
        exp = self._get_token_expiry()
        if exp:
            delay = max(exp - time.time() - 30, 5)
        else:
            delay = 10  # no token yet, poll soon
        self._schedule(delay)

    async def _refresh(self):
        """Fetch fresh access_token from JupyterHub API."""
        try:
            async with httpx.AsyncClient() as client:
                resp = await client.get(
                    f"{self.hub_api_url}/user",
                    headers={"Authorization": f"Bearer {self.hub_token}"},
                    timeout=10,
                )
                resp.raise_for_status()

            auth_state = resp.json().get("auth_state", {})
            access_token = auth_state.get("access_token")
            if not access_token:
                self._schedule(10)  # retry
                return

            self._write_token(access_token)

            # Schedule next refresh based on token expiry
            exp = self._decode_exp(access_token)
            if exp:
                delay = max(exp - time.time() - 30, 5)
            else:
                delay = 240  # fallback: 4 minutes
            self._schedule(delay)

        except Exception:
            # Exponential backoff on failure
            self._schedule(min(self._backoff(), 60))

    def _decode_exp(self, token: str) -> float | None:
        """Decode exp from JWT payload without verification."""
        try:
            payload = token.split(".")[1]
            # Add padding
            payload += "=" * (4 - len(payload) % 4)
            data = json.loads(base64.urlsafe_b64decode(payload))
            return data.get("exp")
        except Exception:
            return None

    def _write_token(self, access_token: str):
        """Write access_token + expires_at to connections.json."""
        exp = self._decode_exp(access_token)
        # Update managed connection in connections.json
        config = _load_config()
        for conn in config.get("connections", []):
            if conn.get("name") == self.connection_name and conn.get("managed"):
                conn["tokens"] = {
                    "access_token": access_token,
                    "expires_at": int(exp) if exp else 0,
                }
                break
        _save_config(config)
```

#### 2.2 Modified: `hugr_connection_service/__init__.py`

Add hub mode initialization:

```python
def _load_jupyter_server_extension(server_app):
    setup_handlers(server_app.web_app)

    # Hub mode: create managed connection from env vars
    hugr_url = os.environ.get("HUGR_URL")
    initial_token = os.environ.get("HUGR_INITIAL_ACCESS_TOKEN")

    if hugr_url:
        connection_name = os.environ.get("HUGR_CONNECTION_NAME", "default")
        _ensure_managed_connection(connection_name, hugr_url)

        provider = HubTokenProvider(connection_name, hugr_url, initial_token)
        provider.start()

    # Restore user's own OIDC sessions (non-managed connections)
    oidc.restore_sessions_on_startup()
```

```python
def _ensure_managed_connection(name: str, hugr_base_url: str):
    """Create or update managed connection in connections.json.

    Called on EVERY container start (new or existing).
    Always overwrites URL from env — ensures config matches current Hub settings.
    Clears stale tokens if URL changed (token from old Hugr is invalid).
    Appends /ipc to base URL — kernels and hugr-client use the IPC endpoint.
    """
    ipc_url = hugr_base_url.rstrip("/") + "/ipc"

    config = _load_config()
    connections = config.get("connections", [])

    for conn in connections:
        if conn.get("name") == name and conn.get("managed"):
            old_url = conn.get("url")
            conn["url"] = ipc_url
            config["default"] = name

            # URL changed → clear stale tokens (issued for a different Hugr)
            if old_url != ipc_url:
                conn.pop("tokens", None)
                logger.info(f"Managed connection URL changed: {old_url} → {ipc_url}, tokens cleared")

            _save_config(config)
            return

    # First start — create new managed connection
    connections.append({
        "name": name,
        "url": ipc_url,
        "auth_type": "hub",
        "managed": True,
    })
    config["connections"] = connections
    config["default"] = name
    _save_config(config)
```

#### 2.3 Modified: `hugr_connection_service/handlers.py`

**ProxyHandler** — add `auth_type == "hub"`:

```python
# In ProxyHandler.post(), after existing auth_type checks:
elif auth_type == "hub":
    # Read token from tokens block (refreshed by HubTokenProvider)
    token = conn.get("tokens", {}).get("access_token")
    if token:
        headers["Authorization"] = f"Bearer {token}"
    else:
        self.set_status(401)
        self.write({"error": "Hub token not yet available, please wait..."})
        return
```

**ConnectionsHandler.post()** — reject creating connection with managed name:

```python
def post(self):
    data = json.loads(self.request.body)
    name = data.get("name")

    # Check if name conflicts with managed connection
    config = _load_config()
    for conn in config.get("connections", []):
        if conn.get("name") == name and conn.get("managed"):
            self.set_status(409)
            self.write({"error": f"Name '{name}' is reserved for hub-managed connection"})
            return
    # ... existing logic
```

**ConnectionHandler.put() / .delete()** — reject for managed connections:

```python
def delete(self, connection_name):
    config = _load_config()
    for conn in config.get("connections", []):
        if conn.get("name") == connection_name and conn.get("managed"):
            self.set_status(403)
            self.write({"error": "Cannot delete hub-managed connection"})
            return
    # ... existing logic

def put(self, connection_name):
    config = _load_config()
    for conn in config.get("connections", []):
        if conn.get("name") == connection_name and conn.get("managed"):
            self.set_status(403)
            self.write({"error": "Cannot modify hub-managed connection"})
            return
    # ... existing logic
```

#### 2.4 Modified: Go kernel `internal/connection/connection.go`

Add `AuthHub` mode:

```go
const (
    AuthPublic     AuthMode = "public"
    AuthAPIKey     AuthMode = "apikey"
    AuthBearer     AuthMode = "bearer"
    AuthBrowser    AuthMode = "browser"
    AuthOIDC       AuthMode = "oidc"
    AuthHub        AuthMode = "hub"        // new
    AuthManagement AuthMode = "management" // new (for future)
)
```

`AuthHub` behaves like `AuthBrowser` — reads token from connections.json, re-reads on expiry. No new methods needed, just handle the new auth_type string in `loadConnectionsFromFile()`.

#### 2.5 Modified: Go kernel `cmd/hugr-kernel/main.go`

In `loadConnectionsFromFile()`, add `"hub"` case:

```go
case "hub", "browser":
    if cfg.Connections[i].Tokens != nil {
        conn.SetBrowserToken(
            cfg.Connections[i].Tokens.AccessToken,
            time.Unix(cfg.Connections[i].Tokens.ExpiresAt, 0),
            configPath,
        )
    }
```

Add `managed` field to parsed struct:

```go
var cfg struct {
    Default     string
    Connections []struct {
        Name     string
        URL      string
        AuthType string `json:"auth_type"`
        Managed  bool
        APIKey   string `json:"api_key"`
        Token    string
        Tokens   *struct {
            AccessToken string `json:"access_token"`
            ExpiresAt   int64  `json:"expires_at"`
        }
    }
}
```

#### 2.6 Modified: Go kernel `internal/meta/commands.go`

Reject modification of managed connections:

```go
// In :auth command handler
func (c *Commands) authCmd(args []string) string {
    conn := c.manager.GetDefault()
    if conn.Managed {
        return "Cannot modify auth for hub-managed connection"
    }
    // ... existing logic
}

// In :key, :token, :logout command handlers — same check
```

Add managed flag display to `:connections`:

```go
// In :connections handler
for _, conn := range connections {
    label := ""
    if conn.Managed {
        label = " [managed]"
    }
    // ... format with label
}
```

#### 2.7 Modified: Go kernel `internal/connection/connection.go`

Add `Managed` field:

```go
type Connection struct {
    Name     string
    URL      string
    AuthMode AuthMode
    Managed  bool      // new: hub-managed, read-only
    Timeout  time.Duration
    // ... rest unchanged
}
```

### 3. hugr-client (Python): optional improvement

Add ability to read token from connections.json:

```python
class HugrClient:
    def __init__(self, url=None, api_key=None, token=None, role=None,
                 connection_name=None):  # new parameter
        if connection_name:
            self._load_from_connections(connection_name)
        # ... existing logic

    def _load_from_connections(self, name):
        config_path = os.environ.get("HUGR_CONFIG_PATH", "~/.hugr/connections.json")
        # read connection by name, extract url + token
```

This is optional for M1 — env var `HUGR_TOKEN` still works for short sessions. Connection file reading is better for long sessions.

## Configuration: OIDC Auto-Discovery from Hugr

### Minimal Admin Configuration

The Hub needs only three mandatory settings:

| Variable | Required | Description |
| -------- | -------- | ----------- |
| `HUGR_URL` | Yes | Hugr server **base** URL (e.g., `http://hugr:15000`) — without `/ipc` |
| `OIDC_CLIENT_SECRET` | Yes | Client secret for Hub's OIDC client (confidential) |
| `HUB_BASE_URL` | Yes | Hub's external URL (e.g., `https://hub.example.com`) |

Everything else is auto-discovered.

**Important: URL conventions:**

- `HUGR_URL` = base URL (`http://hugr:15000`) — used for discovery (`/auth/config`) and future MCP (`/mcp`)
- Kernel IPC endpoint = `{HUGR_URL}/ipc` — the Hub appends `/ipc` when creating the managed connection for kernels
- hugr-client also uses `{HUGR_URL}/ipc`

The Hub derives the IPC URL automatically: `managed_connection.url = f"{HUGR_URL}/ipc"`

### Discovery Flow at Hub Startup

```text
JupyterHub starts
     │
     ▼
1. GET {HUGR_URL}/auth/config
   → {"issuer": "http://keycloak/realms/acme",
      "client_id": "hugr",
      "scopes": ["openid", "profile"]}
     │
     ▼
2. GET {issuer}/.well-known/openid-configuration
   → {"authorization_endpoint": "...",
      "token_endpoint": "...",
      "userinfo_endpoint": "...", ...}
     │
     ▼
3. Configure GenericOAuthenticator with discovered endpoints
   Add "offline_access" to scopes (for refresh tokens)
   Use OIDC_CLIENT_SECRET (Hub is confidential client)
     │
     ▼
4. Pass to spawner environment:
   HUGR_URL={HUGR_URL}  (base URL, connection_service appends /ipc)
```

**Note on client_id**: Hugr's `/auth/config` returns the client_id that Hugr uses to **validate** tokens (e.g., `hugr`). The Hub may use the same client_id or a separate one (`hugr-hub`). If separate, set `OIDC_CLIENT_ID` env var to override. If same client is used for both, the discovered `client_id` is used.

### jupyterhub_config.py

```python
from oauthenticator.generic import GenericOAuthenticator
from dockerspawner import DockerSpawner
import os
import httpx

# ===========================================================================
# OIDC Auto-Discovery from Hugr
# ===========================================================================

HUGR_URL = os.environ["HUGR_URL"]  # required
HUB_BASE_URL = os.environ["HUB_BASE_URL"]  # required

def discover_oidc():
    """Discover OIDC configuration from Hugr server."""
    # Step 1: Get OIDC params from Hugr
    hugr_auth = httpx.get(f"{HUGR_URL}/auth/config", timeout=10).json()
    issuer = hugr_auth["issuer"]
    client_id = os.environ.get("OIDC_CLIENT_ID", hugr_auth.get("client_id", "hugr"))
    scopes = hugr_auth.get("scopes", ["openid", "profile"])

    # Ensure offline_access for refresh tokens
    if "offline_access" not in scopes:
        scopes.append("offline_access")
    if "email" not in scopes:
        scopes.append("email")

    # Step 2: Discover OIDC endpoints from provider
    oidc_config = httpx.get(
        f"{issuer}/.well-known/openid-configuration", timeout=10
    ).json()

    return {
        "issuer": issuer,
        "client_id": client_id,
        "scopes": scopes,
        "authorize_url": oidc_config["authorization_endpoint"],
        "token_url": oidc_config["token_endpoint"],
        "userinfo_url": oidc_config["userinfo_endpoint"],
    }

oidc = discover_oidc()

# ===========================================================================
# Authenticator
# ===========================================================================

c.JupyterHub.authenticator_class = GenericOAuthenticator
c.GenericOAuthenticator.oauth_callback_url = f"{HUB_BASE_URL}/hub/oauth_callback"
c.GenericOAuthenticator.authorize_url = oidc["authorize_url"]
c.GenericOAuthenticator.token_url = oidc["token_url"]
c.GenericOAuthenticator.userdata_url = oidc["userinfo_url"]
c.GenericOAuthenticator.client_id = oidc["client_id"]
c.GenericOAuthenticator.client_secret = os.environ["OIDC_CLIENT_SECRET"]
c.GenericOAuthenticator.scope = oidc["scopes"]
c.GenericOAuthenticator.login_service = "Hugr SSO"
c.GenericOAuthenticator.enable_auth_state = True
c.GenericOAuthenticator.refresh_pre_spawn = True
c.GenericOAuthenticator.auth_refresh_age = 120  # Hub refreshes tokens every 2 min
c.GenericOAuthenticator.username_claim = "preferred_username"

# ===========================================================================
# Spawner
# ===========================================================================

c.JupyterHub.spawner_class = DockerSpawner
c.DockerSpawner.image = os.environ.get("SINGLEUSER_IMAGE", "hugr-lab/hub-singleuser:latest")
c.DockerSpawner.network_name = os.environ.get("DOCKER_NETWORK", "hub-network")
c.DockerSpawner.remove = True
c.DockerSpawner.volumes = {"hub-user-{username}": "/home/jovyan/work"}

# ===========================================================================
# Token injection (only access_token, never refresh_token)
# ===========================================================================

async def pre_spawn_hook(spawner, auth_state):
    spawner.environment["HUGR_URL"] = HUGR_URL
    spawner.environment["HUGR_CONNECTION_NAME"] = os.environ.get("HUGR_CONNECTION_NAME", "default")
    if auth_state:
        spawner.environment["HUGR_INITIAL_ACCESS_TOKEN"] = auth_state.get("access_token", "")

c.Spawner.auth_state_hook = pre_spawn_hook

# ===========================================================================
# Roles (server can read auth_state for token polling)
# ===========================================================================

c.JupyterHub.load_roles = [
    {"name": "user", "scopes": ["self", "admin:auth_state!user"]},
    {"name": "server", "scopes": [
        "users:activity!user", "access:servers!server", "admin:auth_state!user",
    ]},
]

# ===========================================================================
# Hub Service notification (optional, for Stage 2+)
# ===========================================================================

HUB_SERVICE_URL = os.environ.get("HUB_SERVICE_URL")
if HUB_SERVICE_URL:
    async def post_auth_hook(authenticator, handler, authentication):
        auth_state = authentication.get("auth_state", {})
        async with httpx.AsyncClient() as client:
            await client.post(f"{HUB_SERVICE_URL}/api/user/login", json={
                "user_id": authentication["name"],
                "user_name": auth_state.get("name", authentication["name"]),
                "role": auth_state.get("role", ""),
                "email": auth_state.get("email", ""),
            })
        return authentication
    c.Authenticator.post_auth_hook = post_auth_hook
```

### Environment Variables Summary

| Variable | Required | Default | Description |
| -------- | -------- | ------- | ----------- |
| `HUGR_URL` | Yes | — | Hugr server base URL (without `/ipc`). Used for OIDC discovery and passed to containers. Connection service appends `/ipc` for kernel connections. |
| `OIDC_CLIENT_SECRET` | Yes | — | OIDC client secret (Hub is confidential client) |
| `HUB_BASE_URL` | Yes | — | Hub external URL for OAuth callback |
| `OIDC_CLIENT_ID` | No | from Hugr `/auth/config` | Override client_id if Hub uses separate OIDC client |
| `JUPYTERHUB_CRYPT_KEY` | Yes | — | Encryption key for auth_state storage |
| `SINGLEUSER_IMAGE` | No | `hugr-lab/hub-singleuser:latest` | Workspace container image |
| `DOCKER_NETWORK` | No | `hub-network` | Docker network for containers |
| `HUGR_CONNECTION_NAME` | No | `default` | Name for managed Hugr connection in workspace |
| `HUB_SERVICE_URL` | No | — | Hub Service URL (Stage 2+) |

### OIDC Provider Setup (Keycloak)

Two options:

**Option A: Reuse existing `hugr` client** — simplest, no new client needed. Set `Access Type: confidential`, add `offline_access` scope, set valid redirect URI for Hub.

**Option B: Separate `hugr-hub` client** — better isolation. Create new client:

| Setting | Value |
| ------- | ----- |
| Client Protocol | openid-connect |
| Access Type | confidential |
| Standard Flow Enabled | ON |
| Valid Redirect URIs | `{HUB_BASE_URL}/hub/oauth_callback` |
| Web Origins | `+` |
| Client Scopes | openid, profile, email, offline_access |
| PKCE Code Challenge Method | S256 (optional, extra security) |

If using Option B, set `OIDC_CLIENT_ID=hugr-hub` in Hub config.

## Docker Compose (Development with OIDC)

```yaml
services:
  hub:
    build: {context: ., dockerfile: Dockerfile.hub}
    environment:
      HUGR_URL: "http://hugr:15000"
      OIDC_CLIENT_SECRET: "${OIDC_CLIENT_SECRET}"
      HUB_BASE_URL: "http://localhost:8000"
      JUPYTERHUB_CRYPT_KEY: "${JUPYTERHUB_CRYPT_KEY}"
      DOCKER_NETWORK: "hub-network"
      # Optional: OIDC_CLIENT_ID if using separate hugr-hub client
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - hub-data:/srv/jupyterhub
    ports: ["8000:8000"]
    networks: [hub-network]

  hugr:
    image: hugr-lab/hugr:latest
    environment:
      OIDC_ISSUER: "${OIDC_ISSUER}"
      OIDC_CLIENT_ID: "${OIDC_CLIENT_ID:-hugr}"
      SECRET_KEY: "${HUGR_SECRET_KEY}"
    volumes: [hugr-data:/data]
    ports: ["15000:15000"]
    networks: [hub-network]

  keycloak:
    image: quay.io/keycloak/keycloak:latest
    command: start-dev
    environment:
      KEYCLOAK_ADMIN: admin
      KEYCLOAK_ADMIN_PASSWORD: admin
    ports: ["18070:8080"]
    networks: [hub-network]

volumes: {hub-data: {}, hugr-data: {}}
networks:
  hub-network: {name: hub-network}
```

### .env.example

```bash
# Hugr
HUGR_URL=http://hugr:15000
HUGR_SECRET_KEY=local-dev-secret

# OIDC (Keycloak)
OIDC_ISSUER=http://keycloak:8080/realms/acme
OIDC_CLIENT_ID=hugr
OIDC_CLIENT_SECRET=change-me

# JupyterHub
HUB_BASE_URL=http://localhost:8000
JUPYTERHUB_CRYPT_KEY=  # generate with: openssl rand -hex 32
```

Note: Hub only needs `HUGR_URL` + `OIDC_CLIENT_SECRET` + `HUB_BASE_URL`. OIDC endpoints are auto-discovered from Hugr. Other vars are for Hugr and Keycloak themselves.

## Docker Images

### Dockerfile.hub

```dockerfile
FROM quay.io/jupyterhub/jupyterhub:4.1

RUN pip install --no-cache-dir \
    oauthenticator \
    dockerspawner \
    httpx

COPY jupyterhub_config.py /srv/jupyterhub/jupyterhub_config.py

CMD ["jupyterhub", "-f", "/srv/jupyterhub/jupyterhub_config.py"]
```

### Dockerfile.singleuser

```dockerfile
# --- Build Go kernels ---
FROM golang:1.23 AS hugr-kernel-builder
WORKDIR /build
COPY hugr-kernel/ .
RUN CGO_ENABLED=1 go build -o /hugr-kernel ./cmd/hugr-kernel/

FROM golang:1.23 AS duckdb-kernel-builder
WORKDIR /build
COPY duckdb-kernel/ .
RUN CGO_ENABLED=1 go build -o /duckdb-kernel ./cmd/duckdb-kernel/

# --- Final image ---
FROM quay.io/jupyterhub/singleuser:4.1

USER root

# Go kernel binaries
COPY --from=hugr-kernel-builder /hugr-kernel /usr/local/bin/
COPY --from=duckdb-kernel-builder /duckdb-kernel /usr/local/bin/

# Python: hugr connection service + extensions
COPY hugr-kernel/ /tmp/hugr-kernel/
RUN pip install --no-cache-dir /tmp/hugr-kernel/ && rm -rf /tmp/hugr-kernel/

# Python: hugr client + data science packages
RUN pip install --no-cache-dir \
    hugr-client \
    pandas pyarrow geopandas \
    matplotlib plotly

# Register kernel specs
RUN mkdir -p /usr/local/share/jupyter/kernels/hugr
RUN mkdir -p /usr/local/share/jupyter/kernels/duckdb
COPY hugr-kernel/kernel/kernel.json /usr/local/share/jupyter/kernels/hugr/
COPY duckdb-kernel/kernel/kernel.json /usr/local/share/jupyter/kernels/duckdb/

# Enable server extension
RUN jupyter server extension enable hugr_connection_service

USER jovyan
WORKDIR /home/jovyan
CMD ["jupyterhub-singleuser"]
```

## Local Development (No OIDC)

### docker-compose.local.yml

```yaml
services:
  jupyter:
    build: {context: ., dockerfile: Dockerfile.singleuser}
    environment:
      HUGR_URL: "http://hugr:15000/ipc"
      HUGR_AUTH_MODE: "management"
      HUGR_SECRET_KEY: "local-dev-secret"
      HUGR_CONNECTION_NAME: "local"
    ports: ["8888:8888"]
    volumes:
      - ./work:/home/jovyan/work
    networks: [hub-network]
    command: jupyter lab --ip=0.0.0.0 --no-browser --NotebookApp.token=''

  hugr:
    image: hugr-lab/hugr:latest
    environment:
      SECRET_KEY: "local-dev-secret"
      ALLOWED_ANONYMOUS: "false"
    volumes: [hugr-data:/data]
    ports: ["15000:15000"]
    networks: [hub-network]

volumes: {hugr-data: {}}
networks: {hub-network: {name: hub-network}}
```

For local dev (no JupyterHub), connection_service detects `HUGR_AUTH_MODE=management` and creates a managed connection using the management secret key instead of OIDC tokens. No token refresh needed — secret key doesn't expire.

## Acceptance Criteria

1. **OIDC auto-discovery works**: Hub starts with only `HUGR_URL` + `OIDC_CLIENT_SECRET` + `HUB_BASE_URL` → discovers all OIDC endpoints from Hugr
2. **OIDC login works**: User clicks login → OIDC provider → redirect → JupyterLab opens
3. **Kernels pre-configured**: All three kernels have working Hugr connection on first launch (no manual :connect needed)
4. **Token refresh works**: Leave notebook open for 1 hour → queries still work (token refreshed automatically based on JWT exp)
5. **No secrets in container**: `env` inside container does not show refresh_token, client_secret, or management key
6. **Managed connection immutable**: User cannot delete, rename, or change auth of the hub connection via UI or meta commands
7. **User connections work**: User can add their own connections to other Hugr instances alongside the managed one
8. **Local dev works**: `docker compose -f docker-compose.local.yml up` → JupyterLab with working Hugr connection (management auth, no OIDC)
9. **Graceful degradation**: If JupyterHub API temporarily unavailable, cached token continues working until expiry; clear error message only after token actually expires
10. **Startup resilience**: If Hugr is unavailable at Hub startup, Hub retries discovery with backoff; if Hugr is unavailable at container spawn, managed connection is created without token and polls until available
