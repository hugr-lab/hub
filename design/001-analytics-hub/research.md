# Research: Analytics Hub — Full Architecture

## Current Architecture

### Hugr Authentication System

Hugr uses a multi-provider authentication model defined in `query-engine/pkg/auth/`. The system supports:

- **OIDC** — validates pre-issued JWT tokens via `go-oidc/v3`. Configured with issuer, client_id, optional cookie support. Claims mapping: `sub` → user_id, `name` → user_name, scopes with `hugr:` prefix → role.
- **API Keys** — header-based (`X-Hugr-Api-Key`), configurable per-key roles.
- **JWT** — direct public-key verification without OIDC discovery.
- **Anonymous** — fallback with configurable default role.
- **Database API Keys** — stored in CoreDB, supports expiration and caching.

Auth middleware (`query-engine/middlewares.go:17-58`) iterates providers, injects `AuthInfo{Role, UserId, UserName, AuthType, AuthProvider, Token}` into request context. Permissions middleware then loads role-based RBAC from CoreDB (`query-engine/pkg/perm/`).

**Key environment variables for OIDC**:
```
OIDC_ISSUER, OIDC_CLIENT_ID, OIDC_TIMEOUT, OIDC_TLS_INSECURE
AUTH_CONFIG_FILE (YAML with full provider config)
```

**Public discovery endpoint**: `GET /auth/config` returns `{issuer, client_id, scopes}` — used by kernels and IDE extensions to auto-discover OIDC parameters.

### Hugr Kernel (GraphQL)

Location: `hugr-kernel/`

- **Go kernel** communicates via Jupyter Wire Protocol (ZMQ).
- **Python server extension** (`hugr_connection_service/`) manages connections, OIDC login, and token refresh.
- **OIDC flow** (`hugr_connection_service/oidc.py`): Authorization Code + PKCE, auto-discovery via `/auth/config`, refresh token held in memory, access token written to `~/.hugr/connections.json`.
- **Proxy handler** (`hugr_connection_service/handlers.py`): injects `Authorization: Bearer <token>` when proxying requests to Hugr.
- **Auth types**: `public`, `api_key`, `bearer`, `browser` (OIDC).

Design doc: `hugr-kernel/design/oidc-browser-login.md` — comprehensive spec for the OIDC browser login flow.

### DuckDB Kernel

Location: `duckdb-kernel/`

- **Go kernel** with Jupyter Wire Protocol.
- Pure local DuckDB execution — no built-in Hugr auth integration.
- Results as Arrow IPC files in a spool directory.
- Env vars: `DUCKDB_SHARED_SESSION`, `DUCKDB_KERNEL_SPOOL_DIR`, `DUCKDB_KERNEL_SPOOL_TTL`.
- Can attach to Hugr via DuckDB's `httpfs` or `hugr` extension if token is available in environment.

### Python Client (hugr-client)

Location: `hugr-client/hugr/client.py`

- `HugrClient(url, api_key=None, token=None, role=None)`.
- Reads from env: `HUGR_URL`, `HUGR_API_KEY`, `HUGR_TOKEN`.
- Sets `Authorization: Bearer <token>` or `X-Hugr-Api-Key` header.
- Multipart/mixed IPC protocol with Arrow IPC tables.

### MCP Server

Location: `mcp/`

- Custom HTTP transport (`mcp/pkg/auth/transport.go`) injects Bearer token from context.
- Configured with Hugr URL + API key/secret.
- Exposes Hugr data sources as MCP tools for AI agents.

### Docker / Kubernetes Infrastructure

Location: `docker/`

- Dockerfiles for standalone and cluster Hugr deployments.
- Helm chart (`docker/k8s/cluster/`) with management + worker nodes, PostgreSQL CoreDB, Redis cache.
- No existing JupyterHub infrastructure.

## Key Code References

| Reference | Description |
|-----------|-------------|
| `query-engine/pkg/auth/auth.go:1-108` | Auth config, middleware, provider interface |
| `query-engine/pkg/auth/context.go:1-37` | AuthInfo struct, context injection |
| `query-engine/pkg/auth/oidc_provider.go:1-154` | OIDC token validation, claims extraction |
| `query-engine/pkg/perm/permissions.go:166-182` | Auth variables (`[$auth.user_id]`, etc.) for RBAC filters |
| `hugr/cmd/server/config.go:1-176` | Hugr server config loading (env vars, OIDC) |
| `hugr/.local/auth-config.yml` | Example auth config with Keycloak |
| `hugr-kernel/hugr_connection_service/oidc.py:1-481` | Full OIDC Authorization Code + PKCE implementation |
| `hugr-kernel/hugr_connection_service/handlers.py:71-134` | ProxyHandler with token injection |
| `hugr-kernel/design/oidc-browser-login.md` | OIDC browser login design spec |
| `hugr-client/hugr/client.py:555-620` | Python client auth (env vars, headers) |
| `mcp/pkg/auth/transport.go:1-73` | MCP auth transport (Bearer injection) |
| `docker/k8s/cluster/values.yaml` | Kubernetes cluster config reference |

## Patterns & Conventions

1. **Token propagation via env vars**: Both `hugr-client` and kernels read `HUGR_URL`, `HUGR_TOKEN`, `HUGR_API_KEY` from environment. This is the primary integration point for JupyterHub.

2. **OIDC auto-discovery**: All clients discover OIDC parameters via `GET <hugr_url>/auth/config` → then `<issuer>/.well-known/openid-configuration`. No hardcoded OIDC endpoints.

3. **Token ownership model**: Refresh tokens are NEVER persisted to disk — held in memory by the login owner process. Access tokens are written to `~/.hugr/connections.json` with `expires_at`.

4. **Auth header convention**: `Authorization: Bearer <token>` for OIDC/JWT, `X-Hugr-Api-Key` for API keys, `X-Hugr-Role` for role override.

5. **Go for kernels**: Both hugr-kernel and duckdb-kernel are written in Go, communicating via Jupyter Wire Protocol over ZMQ.

6. **Multipart IPC**: Hugr uses a custom multipart/mixed protocol with Arrow IPC tables for efficient data transfer.

## Related Designs

- **`hugr-kernel/design/oidc-browser-login.md`** — The most directly relevant existing design. Defines OIDC flow, token ownership, refresh strategy for the kernel/IDE context. The Hub design must be compatible with this model.
- **`query-engine/design/016-cluster-mode/`** — Cluster auth with internal secrets and node registration.
- **`query-engine/design/020-client-module/`** — Client module patterns.

## External Research

### JupyterHub OIDC Integration

**GenericOAuthenticator** (`oauthenticator` package):
- Supports OIDC providers (Keycloak, EntraID) via manual endpoint configuration.
- `enable_auth_state = True` + `JUPYTERHUB_CRYPT_KEY` enables encrypted token storage in Hub DB.
- `authenticate()` returns `{name, auth_state: {access_token, refresh_token, ...}}`.
- `pre_spawn_start(user, spawner)` hook injects tokens into spawner environment.

**Token Refresh**:
- OAuthenticator 17.2+ supports `refresh_user` — automatic token refresh using refresh_token grant.
- `auth_refresh_age` (default 5min) controls refresh interval.
- `refresh_pre_spawn = True` ensures fresh token before server start.
- Single-user servers can query Hub API for fresh tokens:
  ```python
  GET /hub/user  (with JUPYTERHUB_API_TOKEN)
  → response.json()["auth_state"]["access_token"]
  ```

**Scoped Roles for Token Access**:
```python
c.JupyterHub.load_roles = [
    {"name": "user", "scopes": ["self", "admin:auth_state!user"]},
    {"name": "server", "scopes": ["users:activity!user", "access:servers!server", "admin:auth_state!user"]},
]
```

**DockerSpawner**:
- Spawns single-user Jupyter servers in Docker containers.
- Custom image via `c.DockerSpawner.image`.
- Requirements: image must have `jupyterhub-singleuser` command.
- Supports volume mounts, environment variable injection, network configuration.

### Key Sources

- [OAuthenticator Token Refresh Guide](https://oauthenticator.readthedocs.io/en/latest/how-to/refresh.html)
- [JupyterHub Authenticators Reference](https://jupyterhub.readthedocs.io/en/latest/reference/authenticators.html)
- [DockerSpawner](https://github.com/jupyterhub/dockerspawner)
- [JupyterHub OIDC with Keycloak](https://medium.com/@sraza0098/jupyterhub-with-oidc-keycloak-and-single-user-images-07a0f3a3fc0f)
- [Zero to JupyterHub — Authentication](https://z2jh.jupyter.org/en/stable/administrator/authentication.html)

## Hugr Vector Search & Embeddings

Hugr has built-in vector search support:

- **Vector field type**: `query-engine/pkg/catalog/types/extra_field_vector.go` — generates `_<fieldName>_distance` function with distance metrics (Cosine, Euclidean, Manhattan).
- **Query planning**: `query-engine/pkg/planner/node_select_vector.go` — `VectorDistanceSQL()` for DuckDB and PostgreSQL.
- **Embedding support**: Schema supports `@dim(len: <size>)` directive, CoreDB runtime includes `import_descriptions()` with `include_embeddings` and `recompute_embeddings` parameters.
- **pgvector infrastructure**: Kubernetes values use `pgvector/pgvector:pg16` image for CoreDB. MCP config includes `embedderURL` and `vectorSize: 768`.
- **Embedder service**: External embedder URL configurable via `management.mcp.embedderURL`.

This means Hugr can serve as the vector store for agent memory — store embeddings alongside text content, query via vector distance.

## Hugr MCP Server Tools

Location: `mcp/pkg/service/`

10 tools exposed via MCP protocol:

**Discovery** (semantic search over schema):
1. `discovery-search_modules` — find modules by natural language query
2. `discovery-search_data_sources` — find data sources
3. `discovery-search_module_data_objects` — find tables/views in a module
4. `discovery-search_module_functions` — find functions/mutations
5. `discovery-data_object_field_values` — get field statistics

**Schema introspection**:
6. `schema-type_info` — detailed type information
7. `schema-type_fields` — list fields of a type
8. `schema-enum_values` — list enum values

**Data execution**:
9. `data-inline_graphql_result` — execute GraphQL with optional jq transform
10. `data-validate_graphql_query` — validate without executing

All tools use the indexer service for caching and vector-based semantic search when embeddings are enabled.

**Resources**: Static reference.json embedded via `embed.FS`.
**Prompts**: Template prompts with arguments.

## MotherDuck Dives (Reference Analysis)

**What Dives are**: Interactive React + SQL data apps that sit on top of live database queries. Created by AI agents via MCP, persisted in workspaces, shareable via URL.

**Technical structure**:
- React components with SQL queries.
- `REQUIRED_DATABASES` constant for dependency declaration.
- Versioned (numbered versions accessible via MCP tools).
- MCP tools: `list_dives`, `read_dive`, `share_dive_data`.

**Sharing model**: `share_dive_data` creates org-scoped shares for private databases. Viewers see live query results without manual permission management.

**Key insight for our design**: Dives are essentially React apps where queries execute against the viewer's database access. For Hugr, this means GraphQL queries execute with the viewer's OIDC token, ensuring RBAC is respected per viewer.

**Differences from our approach**: MotherDuck Dives use SQL directly; we use GraphQL. MotherDuck has a centralized service; we need per-user isolation with shared publishing.

## OpenClaw (Reference Analysis)

**Architecture**: Four layers — Gateway (background process), Channels (messaging adapters), Skills (modular extensions), Memory (local workspace).

**Skills system**: Directories with `SKILL.md` files. Three loading locations: bundled, managed (`~/.openclaw/skills`), workspace. Precedence: workspace > managed > bundled. Skills injected into system prompt (~97 chars/skill overhead).

**Memory**: Local markdown files reloaded at session start. Configuration and history stored locally. "Cognitive system that tells the agent who it is."

**Security model**: Three-tier sandboxing (off / non-main / all). Allowlist-based tool access. Deny rules take precedence. Per-agent policies. Docker isolation per session.

**Critical lessons for us**:
1. **Supply chain risk**: 12% of ClawHub skills were compromised. We must NOT allow public skill registries — only admin-curated skills.
2. **CVE-2026-25253**: Unvalidated gateway URLs exposed auth tokens. Our MCP proxy must validate all inputs.
3. **Tool policy engine**: Allowlist + deny rules + audit logging is the right pattern.
4. **Skill format**: Markdown-based skill definitions are elegant and work well with LLMs.
5. **Memory as workspace files**: Simple, debuggable, version-controllable. Good for Layer 1.

## Management Auth in hugr-kernel

The Hugr server supports a special "management" API key auth:
- Header: `x-hugr-secret-key` (configured in `hugr/pkg/auth/auth.go:64-76`)
- Default role: `admin`
- Supports role/user_id/user_name override via headers
- Environment variable: `SECRET_KEY`

This is ideal for local development without OIDC. The kernel needs to support this auth type for simplified dev workflows.

## Gaps & Open Questions

### Resolved (in design)

1. **Token refresh in containers** → Resolved: `hugr_connection_service` owns refresh, Hub API as fallback.
2. **Token delivery mechanism** → Resolved: env vars at spawn time, connection service manages lifecycle.
3. **Connection service vs Hub tokens** → Resolved: Hybrid — Hub does OIDC, passes tokens to connection service.
4. **DuckDB kernel Hugr access** → Resolved: `:connect_hugr` meta command + connections.json.

### Open for Dives

5. **Dive bundler toolchain**: Vite seems right but needs validation with Hugr's Perspective viewer integration.
6. **Dive hosting isolation**: Should published dives run in their own container or be served statically?
7. **Offline vs online mode**: How to snapshot data for offline dives without exposing raw data?

### Open for Agent

8. **LLM provider**: Claude API vs self-hosted? Configurable, but what's the default?
9. **Embedding model selection**: Use Hugr's existing embedder? Need to verify API compatibility.
10. **Memory size limits**: How much vector memory per user before it degrades search quality?
11. **Agent session persistence**: Should agent context survive container restart?

### Open for Security

12. **Dive code review**: Manual review before publish? Automated sandboxing? Both?
13. **Agent tool abuse**: Rate limiting per tool? Budget per user per day?
14. **Cross-user memory isolation**: Query registry has public entries — how to prevent data leakage via query patterns?
