# Microsoft Entra ID Setup for Hugr + Analytics Hub

## Overview

Hugr and Analytics Hub (JupyterHub) use a **single App Registration** in Entra ID. The app acts as both:
- **API** — Hugr validates access tokens (audience = app)
- **Web client** — JupyterHub performs Authorization Code Flow with client secret

## Step 1: Create App Registration

1. Azure Portal → Entra ID → App registrations → **New registration**
2. Name: `hugr-dev-jupyter` (or your preferred name)
3. Supported account types: choose based on your org
4. Redirect URI: skip for now
5. Click **Register**

Note the **Application (client) ID** — this is your `OIDC_CLIENT_ID`.

## Step 2: Authentication — Web Platform

1. Go to **Authentication** → **Add a platform** → **Web**
2. Redirect URI: `http://localhost:8000/hub/oauth_callback`
   - Production: `https://hub.yourdomain.com/hub/oauth_callback`
3. Leave other settings as default
4. **Save**

> **Important:** Do NOT use SPA platform. JupyterHub is a server-side app and requires client secret in token exchange, which SPA does not support.

## Step 3: Client Secret

1. Go to **Certificates & secrets** → **New client secret**
2. Description: `hub-secret`
3. Expiry: choose appropriate
4. **Copy the secret value** immediately — this is your `OIDC_CLIENT_SECRET`

## Step 4: Expose an API

This is required so that access tokens have the correct `aud` (audience) claim. Without this, Entra issues tokens for Microsoft Graph (`aud: 00000003-...`) which Hugr rejects.

1. Go to **Expose an API**
2. **Set** Application ID URI → `api://<client-id>` (e.g., `api://0089fa79-b0f6-4c5c-bffa-8e62744bf277`)
3. **Add a scope:**
   - Scope name: `access`
   - Who can consent: **Admins and users**
   - Admin consent display name: `Access Hugr`
   - Admin consent description: `Allow access to Hugr data platform`
   - State: **Enabled**
4. **Add a client application:**
   - Client ID: paste the same Application (client) ID
   - Authorized scopes: check `access`

## Step 5: Token Version

Entra v1.0 tokens use issuer `https://sts.windows.net/{tenant}/`, while Hugr expects the v2.0 issuer from OIDC discovery (`https://login.microsoftonline.com/{tenant}/v2.0`). Force v2.0 tokens:

1. Go to **Manifest** (or **Expose an API** depending on portal version)
2. Find `requestedAccessTokenVersion` (in the `api` section)
3. Set to `2`
4. **Save**

## Step 6: API Permissions (optional)

If the scope is set to "Admins only" consent, grant admin consent:

1. Go to **API permissions**
2. Verify `User.Read` (Microsoft Graph) is present (default)
3. If you see a warning about admin consent, click **Grant admin consent for [org]**

## Hugr Configuration

Hugr needs to know the OIDC issuer and client ID. Typical config:

```yaml
# Hugr server config
OIDC_ISSUER: "https://login.microsoftonline.com/{tenant-id}/v2.0"
OIDC_CLIENT_ID: "<application-client-id>"
```

Hugr will:
- Fetch JWKS from `https://login.microsoftonline.com/{tenant-id}/discovery/v2.0/keys`
- Validate `aud` = `api://<client-id>` in access tokens
- Validate `iss` = `https://login.microsoftonline.com/{tenant-id}/v2.0`

## Analytics Hub (JupyterHub) Configuration

`.env` file:

```env
# Hugr server URL (accessible from Hub container)
HUGR_URL=http://host.docker.internal:15004

# Entra OIDC
OIDC_CLIENT_ID=<application-client-id>
OIDC_CLIENT_SECRET=<client-secret-value>

# Hub external URL (must match redirect URI in Entra)
HUB_BASE_URL=http://localhost:8000

# Entra-specific: use email as username (Entra doesn't return preferred_username via id_token)
OIDC_USERNAME_CLAIM=email

# Entra-specific: get user info from id_token (Entra userinfo endpoint requires Graph token)
OIDC_USERDATA_FROM_ID_TOKEN=1

# Entra-specific: request custom API scope for correct audience in access token
OIDC_EXTRA_SCOPES=api://<application-client-id>/access

# JupyterHub encryption key (generate with: openssl rand -hex 32)
JUPYTERHUB_CRYPT_KEY=<generated-key>

# Singleuser image
SINGLEUSER_IMAGE=hub-jupyter:latest
DOCKER_NETWORK=hub-dev-network
```

### Why these Entra-specific settings?

| Setting | Reason |
|---------|--------|
| `OIDC_USERNAME_CLAIM=email` | Entra id_token doesn't include `preferred_username` in userinfo response; `email` is available |
| `OIDC_USERDATA_FROM_ID_TOKEN=1` | Entra userinfo endpoint (`graph.microsoft.com/oidc/userinfo`) requires a Graph-scoped token, but our token has API scope audience — 401 conflict |
| `OIDC_EXTRA_SCOPES=api://.../access` | Without this, access token `aud` = Microsoft Graph, which Hugr rejects. Custom scope forces `aud` = your app |

## Token Flow

```
Browser → JupyterHub → Entra (authorize)
                     ← code
         JupyterHub → Entra (token exchange: code + client_secret)
                     ← access_token (aud=api://app, iss=v2.0) + id_token + refresh_token
         JupyterHub → spawns workspace container with access_token
                     → workspace polls JupyterHub API for fresh tokens
         Workspace  → Hugr (Authorization: Bearer <access_token>)
                     ← data
```

## Troubleshooting

### 500 on /hub/oauth_callback

**Token exchange 401:**
- Check client secret is valid and not expired
- Ensure redirect URI is registered as **Web** (not SPA)
- Verify `OIDC_CLIENT_ID` matches App Registration

**Userinfo 401:**
- Add `OIDC_USERDATA_FROM_ID_TOKEN=1` to env

**"No preferred_username found":**
- Add `OIDC_USERNAME_CLAIM=email` to env

### Hugr rejects token

**"id token issued by a different provider":**
- Set `requestedAccessTokenVersion` to `2` in Entra manifest

**Wrong audience (`aud: 00000003-...`):**
- Configure API scope in Expose an API (Step 4)
- Add `OIDC_EXTRA_SCOPES=api://<client-id>/access` to env

### Hub startup hangs ("OIDC discovery failed")

- Hugr server is not running or not reachable from Hub container
- Check `HUGR_URL` and that `host.docker.internal` resolves

## Keycloak Comparison

| Aspect | Keycloak | Entra ID |
|--------|----------|----------|
| Clients | Separate: `hugr` (public) + `hugr-hub` (confidential) | Single App Registration (API + Web) |
| Token audience | Configured per client | Requires "Expose an API" + custom scope |
| Userinfo | Standard OIDC endpoint | Graph endpoint (incompatible with custom audience) |
| Username claim | `preferred_username` | `email` (via id_token) |
| Token version | N/A | Must set `requestedAccessTokenVersion: 2` |
| Refresh tokens | `offline_access` scope | `offline_access` scope (same) |
