# Design: Analytics Hub — Target Architecture

## Summary

A corporate analytics hub providing isolated user environments with Jupyter kernels, an AI agent assistant, interactive data visualizations (Dives), and periodic task scheduling. All data access flows through Hugr under the user's identity, enforced by Hugr RBAC.

## Architecture Principles

1. **Hub does NOT manage Hugr.** Hugr is an external service. Hub connects as a client. One Hugr can serve multiple Hubs.
2. **Everything executes under user identity.** Kernels, agent, dives, scheduled tasks — Hugr RBAC enforces data security per user.
3. **Two auth models.** Workspace (interactive) uses OIDC tokens directly. Agent/scheduler (programmatic) uses Hub Service management secret with user identity headers — no token expiry, works when user is offline.
4. **Hub Service is a Hugr application.** Hub metadata (memory, query registry, dives, scheduler) lives in Hugr as an Airport data source — accessible via the same GraphQL API.
5. **Separation of concerns.** JupyterHub manages workspaces. Hub Service manages agents, hub data, and system tasks. Neither intrudes on the other.
6. **Agent is isolated.** Agent runs in a separate container with restricted filesystem access. Sandboxed tools (Python, bash) execute inside the agent container. Hugr access goes through Hub Service (trusted intermediary).
7. **Agent has no credentials.** Agent connects to Hub Service as MCP client. Hub Service adds auth. Secret key exists only in Hub Service.

## Authentication Architecture

### Two Auth Paths

```text
PATH 1: Workspace (interactive, OIDC)
  User → Browser → JupyterHub (OIDC login)
       → Workspace Container (OIDC tokens)
       → Hugr: Authorization: Bearer <user_token>

  User's own token. Refreshed by connection_service.
  Standard OIDC flow. Works while user session is active.

PATH 2: Agent + Scheduler (programmatic, management secret)
  Agent → Hub Service (MCP client, no credentials)
        → Hugr: x-hugr-secret-key: <secret>
                x-hugr-user-id: alice
                x-hugr-role: analyst
                x-hugr-user-name: Alice Smith

  Hub Service is trusted intermediary. Knows user identity from login.
  Management secret never expires. Agent works even when user is offline.
```

### Why Two Paths

| | OIDC (workspace) | Management secret (agent) |
| --- | --- | --- |
| Token expiry | Yes, needs refresh chain | No, secret is permanent |
| User offline | Stops working eventually | Works forever |
| Identity source | JWT claims | Hub Service DB (from login) |
| Credentials in container | Yes (refresh token) | No (only in Hub Service) |
| Use case | Interactive sessions | Autonomous agent, scheduler |

Hugr already supports both. Management auth (`x-hugr-secret-key`) accepts identity override headers (`x-hugr-role`, `x-hugr-user-id`, `x-hugr-user-name`). RBAC applies identically — Hugr doesn't care how identity was established.

### Security: Why This Is Safe

- Management secret lives **only** in Hub Service, never in user containers or agent containers.
- Agent cannot forge identity — it has no secret, all requests go through Hub Service.
- Hub Service sets identity based on its own DB (populated at user login), not from agent input.
- Hugr validates the secret before trusting identity headers.
- Audit: Hub Service logs every request with user_id + tool + arguments.

## Three-Layer Architecture

```text
┌─────────────────────────────────────────────────────────────────────────┐
│                        OIDC Provider (Keycloak / EntraID)               │
└──────────┬──────────────────────────────────────────────────────────────┘
           │
┌──────────▼──────────────────────────────────────────────────────────────┐
│  CONTROL PLANE                                                          │
│                                                                         │
│  ┌──────────────┐     ┌──────────────────────────────────────────────┐ │
│  │ JupyterHub   │     │ Hub Service (Go)                             │ │
│  │              │     │                                              │ │
│  │ OIDC auth    │────>│ POST /api/user/login                        │ │
│  │ DockerSpawner│     │ {user_id, user_name, role, email}           │ │
│  │ (workspace)  │     │                                              │ │
│  │              │     │ ┌────────────┐ ┌──────────┐ ┌────────────┐ │ │
│  │ Proxy:       │     │ │ Agent Mgr  │ │ Flight   │ │ System     │ │ │
│  │ /user/*      │     │ │ Docker/K8s │ │ Server   │ │ Scheduler  │ │ │
│  │  → ws:8888   │     │ └────────────┘ │ hub.*    │ └────────────┘ │ │
│  │              │     │                └──────────┘                  │ │
│  │              │     │ ┌──────────────────────────────────────────┐ │ │
│  │              │     │ │ MCP Server (for agents)                  │ │ │
│  │              │     │ │ ├── Hugr tools (+ secret + identity)    │ │ │
│  │              │     │ │ └── hub.* tools (local execution)       │ │ │
│  │              │     │ └──────────────────────────────────────────┘ │ │
│  │              │     │                                              │ │
│  │              │     │ Auth: HUGR_SECRET_KEY (management)           │ │
│  │              │     │ Hub DB (PostgreSQL + pgvector)               │ │
│  └──────────────┘     └──────────────────────────────────────────────┘ │
│                                                                         │
└─────────────────────────┬──────────────────────┬────────────────────────┘
                          │                      │
                    Docker/K8s API         Arrow Flight +
                          │                Management Auth
                          │                      │
┌─────────────────────────▼──────────────────────▼────────────────────────┐
│  USER PLANE (per user)                                                   │
│                                                                          │
│  PVC: hub-alice-work                                                     │
│  ├── notebooks/          (workspace: rw, agent: no access)               │
│  ├── shared/             (workspace: rw, agent: rw)                      │
│  │   ├── results/                                                        │
│  │   ├── dives/                                                          │
│  │   └── data/                                                           │
│  └── .agent/             (workspace: ro, agent: rw)                      │
│      ├── memory/                                                         │
│      ├── skills/                                                         │
│      └── logs/                                                           │
│                                                                          │
│  ┌─ alice-workspace ─────────────┐  ┌─ alice-agent ──────────────────┐  │
│  │ JupyterLab + Kernels         │  │ hub-agent (Go binary)          │  │
│  │ connection_service            │  │                                │  │
│  │                               │  │ MCP Client → Hub Service      │  │
│  │ Auth: OIDC tokens             │  │   (hugr tools, hub.* tools)   │  │
│  │ (Bearer token to Hugr)        │  │                                │  │
│  │                               │  │ Local tools (in-process):     │  │
│  │ Managed by: JupyterHub       │  │   python, bash, web, files    │  │
│  │                               │  │                                │  │
│  │ Mounts: full PVC              │  │ Auth: NONE (no credentials)   │  │
│  └───────────────────────────────┘  │ Managed by: Hub Service       │  │
│                                     │                                │  │
│                                     │ Mounts: shared/ + .agent/     │  │
│                                     └────────────────────────────────┘  │
│                                                                          │
└──────────────────────────┬───────────────────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────────────────┐
│  DATA PLANE                                                               │
│                                                                           │
│  Hugr Server                                                              │
│  ├── customer data (DuckDB, Postgres, Iceberg, ...)                       │
│  ├── hub.* (Airport source → Hub Service)                                 │
│  │   ├── hub.memory.agent_memories (vector search)                        │
│  │   ├── hub.registry.queries                                             │
│  │   ├── hub.dives.published_dives                                        │
│  │   ├── hub.scheduler.tasks                                              │
│  │   └── hub.workspace.agents (status, management)                        │
│  ├── GraphQL API (unified, all sources)                                   │
│  └── MCP endpoint (indexes hub.* alongside customer data)                 │
│                                                                           │
│  Auth accepts:                                                            │
│  - Authorization: Bearer <OIDC token>  (workspace path)                   │
│  - x-hugr-secret-key + identity headers  (agent/scheduler path)           │
│  Both → same AuthInfo → same RBAC                                         │
└───────────────────────────────────────────────────────────────────────────┘
```

## Agent ↔ Hub Service Communication

### Agent as MCP Client

The agent container has **no credentials**. It connects to Hub Service as an MCP client. Hub Service is the MCP server that provides all non-local tools.

```text
┌─ Agent Container ──────────────────────────────────────────────┐
│                                                                │
│  hub-agent process (Go)                                        │
│  ├── LLM Client (Claude API / configurable)                    │
│  │                                                             │
│  ├── MCP Client → Hub Service:8080/mcp/{user_id}              │
│  │   Hub Service provides:                                     │
│  │   ├── discovery-search_modules        ─┐                    │
│  │   ├── discovery-search_data_sources    │                    │
│  │   ├── discovery-search_module_objects  ├─ forwarded to Hugr │
│  │   ├── schema-type_info                 │  with secret +     │
│  │   ├── schema-type_fields               │  user identity     │
│  │   ├── data-inline_graphql_result       │                    │
│  │   ├── data-validate_graphql_query     ─┘                    │
│  │   ├── hub.memory-store                ─┐                    │
│  │   ├── hub.memory-search                ├─ executed locally  │
│  │   ├── hub.registry-register            │  in Hub Service    │
│  │   ├── hub.registry-search              │  (Flight Server)   │
│  │   ├── hub.dive-publish                ─┘                    │
│  │   └── hub.workspace-status                                  │
│  │                                                             │
│  └── Local MCP Server (in-process, no network)                 │
│      ├── python_exec      (execute Python code in sandbox)     │
│      ├── bash_exec        (execute shell command in sandbox)   │
│      ├── web_search       (internet search)                    │
│      ├── web_fetch        (download page)                      │
│      ├── file_read        (/workspace/shared/, .agent/)        │
│      ├── file_write       (/workspace/shared/, .agent/)        │
│      └── file_list        (/workspace/shared/, .agent/)        │
│                                                                │
│  Tool Policy: applied by hub-agent before any tool call        │
└────────────────────────────────────────────────────────────────┘
```

### Hub Service MCP Server

Hub Service exposes per-user MCP endpoints. Each endpoint knows the user identity and adds auth automatically:

```text
Hub Service
│
├── MCP endpoint: /mcp/alice
│   All tool calls → Hugr with: secret + alice identity
│
├── MCP endpoint: /mcp/bob
│   All tool calls → Hugr with: secret + bob identity
│
└── How it works:
    1. Agent connects to /mcp/{user_id}
    2. Agent calls tool: data-inline_graphql_result({query: "..."})
    3. Hub Service:
       a. Checks tool policy for this user
       b. Logs: {user: alice, tool: data-inline_graphql_result, args: ...}
       c. Forwards to Hugr MCP with headers:
          x-hugr-secret-key: <HUGR_SECRET_KEY>
          x-hugr-user-id: alice
          x-hugr-role: analyst
       d. Returns result to agent
```

### Data Flow: Agent Queries Hugr Data

```text
Agent         Hub Service           Hugr
  │                │                  │
  │  MCP: data-    │                  │
  │  inline_       │                  │
  │  graphql_      │                  │
  │  result        │                  │
  │  {query:       │                  │
  │   "{ sales     │                  │
  │   .orders...}"}│                  │
  │───────────────>│                  │
  │                │  POST /mcp       │
  │                │  Headers:        │
  │                │   x-hugr-secret  │
  │                │   x-hugr-user-id │
  │                │   x-hugr-role    │
  │                │  Body: tool call │
  │                │─────────────────>│
  │                │                  │  Validate secret ✓
  │                │                  │  AuthInfo{alice, analyst}
  │                │                  │  Apply RBAC
  │                │                  │  Execute query
  │                │  Results         │
  │                │<─────────────────│
  │  Results       │                  │
  │<───────────────│                  │
```

### Data Flow: Agent Stores Memory

```text
Agent         Hub Service                          Hub DB
  │                │                                  │
  │  MCP:          │                                  │
  │  hub.memory-   │                                  │
  │  store         │                                  │
  │  {content:..., │                                  │
  │   type:...}    │                                  │
  │───────────────>│                                  │
  │                │  Executed locally (no Hugr hop): │
  │                │  INSERT INTO agent_memories      │
  │                │  (user_id='alice', ...)          │
  │                │─────────────────────────────────>│
  │                │                                  │
  │  {id: "..."}   │                                  │
  │<───────────────│                                  │
```

Hub.* tools are executed directly by Hub Service (it owns the data). No round-trip through Hugr for these.

Note: the data is still visible through Hugr GraphQL (via Flight) for queries from workspace kernels or external clients. But mutations from the agent go directly to Hub DB for efficiency.

## Hub Service as Hugr Application (Airport-Go)

Hub Service is a Go service built on `airport-go` that registers with Hugr as an Arrow Flight data source.

### Hugr Configuration

```yaml
data_sources:
  - name: hub
    type: airport
    dsn: "grpc://hub-service:50051"
    auth_token: "${HUGR_SECRET_KEY}"  # Hub Service validates this
```

### Flight Catalog

```text
Catalog: hub
├── Schema: memory
│   ├── Table: agent_memories
│   │   Fields: id, user_id, type, title, content, tags,
│   │           embedding (Vector @dim(768)), created_at
│   │   RBAC: user_id = [$auth.user_id]
│   │
│   └── Functions:
│       ├── store(content, type, title, tags) → AgentMemory
│       ├── search(query, type?, limit?) → [AgentMemory]
│       └── forget(id) → Boolean
│
├── Schema: registry
│   ├── Table: queries
│   │   Fields: id, author, name, description, query,
│   │           variables_schema, tags, version, is_public, embedding
│   │   RBAC: author = [$auth.user_id] OR is_public = true
│   │
│   └── Functions:
│       ├── register(name, query, description?, tags?, is_public?) → QueryEntry
│       └── search(query, limit?) → [QueryEntry]
│
├── Schema: dives
│   ├── Table: published_dives
│   │   Fields: id, author, name, title, description, version,
│   │           bundle_path, visibility, created_at
│   ├── Table: dive_shares
│   │   Fields: dive_id, user_id, permission
│   │
│   └── Functions:
│       ├── publish(name, title, bundle, visibility) → Dive
│       └── share(dive_id, user_id, permission) → DiveShare
│
├── Schema: scheduler
│   ├── Table: tasks
│   │   Fields: id, user_id, name, schedule, type, config,
│   │           scope (user/system), last_run, next_run, status
│   ├── Table: task_runs
│   │   Fields: id, task_id, started_at, finished_at, status, result
│   │
│   └── Functions:
│       ├── create_task(name, schedule, type, config, scope?) → Task
│       └── cancel_task(id) → Boolean
│
└── Schema: workspace
    ├── Table: agents
    │   Fields: user_id, status, container_id, created_at, last_activity
    │   RBAC: user_id = [$auth.user_id] (admin sees all)
    │
    ├── Table: users
    │   Fields: user_id, user_name, role, email, last_login
    │
    └── Functions:
        ├── start_agent() → Agent
        └── stop_agent() → Agent
```

### Example Queries via Hugr GraphQL

```graphql
# From workspace kernel or external client — goes through Hugr
query {
  hub {
    memory {
      agent_memories(
        filter: { type: { eq: "schema" } }
        order_by: { _embedding_distance: { vector: $query_vec, metric: Cosine } }
        limit: 5
      ) {
        title
        content
        tags
      }
    }
    registry {
      queries(filter: { is_public: { eq: true } }) {
        name
        query
        description
      }
    }
    workspace {
      agents { user_id status last_activity }
    }
  }
}

# User starts their agent
mutation {
  hub { workspace { start_agent { status } } }
}
```

---

## Milestone 1: JupyterHub + OIDC + Kernels

Standard JupyterHub deployment. No Hub Service required.

### Components

- JupyterHub with GenericOAuthenticator (OIDC) + DockerSpawner
- Single-user image with Hugr kernel, DuckDB kernel, Python kernel
- Token propagation via env vars + hugr_connection_service refresh

### JupyterHub Configuration

```python
from oauthenticator.generic import GenericOAuthenticator
from dockerspawner import DockerSpawner
import os

# --- Authenticator ---
c.JupyterHub.authenticator_class = GenericOAuthenticator
c.GenericOAuthenticator.oauth_callback_url = os.environ["OAUTH_CALLBACK_URL"]
c.GenericOAuthenticator.authorize_url = os.environ["OIDC_AUTHORIZE_URL"]
c.GenericOAuthenticator.token_url = os.environ["OIDC_TOKEN_URL"]
c.GenericOAuthenticator.userdata_url = os.environ["OIDC_USERINFO_URL"]
c.GenericOAuthenticator.client_id = os.environ["OIDC_CLIENT_ID"]
c.GenericOAuthenticator.client_secret = os.environ["OIDC_CLIENT_SECRET"]
c.GenericOAuthenticator.scope = ["openid", "profile", "email", "offline_access"]
c.GenericOAuthenticator.login_service = "Hugr SSO"
c.GenericOAuthenticator.enable_auth_state = True
c.GenericOAuthenticator.refresh_pre_spawn = True
c.GenericOAuthenticator.auth_refresh_age = 240
c.GenericOAuthenticator.username_claim = "preferred_username"

# --- Spawner ---
c.JupyterHub.spawner_class = DockerSpawner
c.DockerSpawner.image = os.environ.get("SINGLEUSER_IMAGE", "hugr-lab/hub-singleuser:latest")
c.DockerSpawner.network_name = os.environ.get("DOCKER_NETWORK", "hub-network")
c.DockerSpawner.remove = True
c.DockerSpawner.volumes = {"hub-user-{username}": "/home/jovyan/work"}
c.DockerSpawner.environment = {"HUGR_URL": os.environ["HUGR_URL"]}

# --- Token injection ---
async def pre_spawn_hook(spawner, auth_state):
    if auth_state:
        spawner.environment["HUGR_OIDC_ACCESS_TOKEN"] = auth_state.get("access_token", "")
        spawner.environment["HUGR_OIDC_REFRESH_TOKEN"] = auth_state.get("refresh_token", "")
        spawner.environment["HUGR_OIDC_TOKEN_URL"] = os.environ["OIDC_TOKEN_URL"]
        spawner.environment["HUGR_OIDC_CLIENT_ID"] = os.environ["OIDC_CLIENT_ID"]
        spawner.environment["HUGR_OIDC_CLIENT_SECRET"] = os.environ["OIDC_CLIENT_SECRET"]

c.Spawner.auth_state_hook = pre_spawn_hook

# --- Roles ---
c.JupyterHub.load_roles = [
    {"name": "user", "scopes": ["self", "admin:auth_state!user"]},
    {"name": "server", "scopes": [
        "users:activity!user", "access:servers!server", "admin:auth_state!user",
    ]},
]

# --- Hub Service notification (M2+, optional) ---
HUB_SERVICE_URL = os.environ.get("HUB_SERVICE_URL")
if HUB_SERVICE_URL:
    import httpx

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

**Note**: `post_auth_hook` sends user identity to Hub Service but **not tokens**. Hub Service doesn't need OIDC tokens — it uses management secret. It only needs to know who logged in and what role they have.

### Token Lifecycle in Workspace

```text
Container Start
     │
     ▼
hugr_connection_service reads env vars:
  HUGR_OIDC_ACCESS_TOKEN, HUGR_OIDC_REFRESH_TOKEN,
  HUGR_OIDC_TOKEN_URL, HUGR_OIDC_CLIENT_ID, HUGR_OIDC_CLIENT_SECRET
     │
     ├── Creates LoginSession (refresh_token in memory)
     ├── Writes access_token to ~/.hugr/connections.json
     ├── Starts refresh timer (expires_at - 30s)
     │
     ▼
Token refresh loop:
     ├── POST token_url (grant_type=refresh_token)
     ├── Success: update connections.json, reschedule
     ├── Failure: fallback to Hub API
     │     GET {JUPYTERHUB_API_URL}/hub/user
     │     extract fresh tokens from auth_state
     └── Total failure: notify user "session expired"
```

### Changes to hugr-kernel

| Change | File | Description |
| ------ | ---- | ----------- |
| Hub mode init | `hugr_connection_service/__init__.py` | Read env vars, create default connection on startup |
| client_secret | `hugr_connection_service/oidc.py` | LoginSession accepts client_secret for confidential client |
| Management auth | `hugr_connection_service/handlers.py` | Support `x-hugr-secret-key` header in proxy |
| Auth mode env | `hugr_connection_service/__init__.py` | `HUGR_AUTH_MODE`: oidc, api_key, management |

### Docker Compose (Development with OIDC)

```yaml
services:
  hub:
    build: {context: ., dockerfile: Dockerfile.hub}
    environment:
      JUPYTERHUB_CRYPT_KEY: "${JUPYTERHUB_CRYPT_KEY}"
      OIDC_AUTHORIZE_URL: "${OIDC_ISSUER}/protocol/openid-connect/auth"
      OIDC_TOKEN_URL: "${OIDC_ISSUER}/protocol/openid-connect/token"
      OIDC_USERINFO_URL: "${OIDC_ISSUER}/protocol/openid-connect/userinfo"
      OAUTH_CALLBACK_URL: "http://localhost:8000/hub/oauth_callback"
      OIDC_CLIENT_ID: "${OIDC_CLIENT_ID}"
      OIDC_CLIENT_SECRET: "${OIDC_CLIENT_SECRET}"
      HUGR_URL: "http://hugr:15000/ipc"
      DOCKER_NETWORK: "hub-network"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - hub-data:/srv/jupyterhub
    ports: ["8000:8000"]
    networks: [hub-network]

  hugr:
    image: hugr-lab/hugr:latest
    environment:
      OIDC_ISSUER: "${OIDC_ISSUER}"
      OIDC_CLIENT_ID: "${OIDC_CLIENT_ID}"
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

### Local Dev (No OIDC)

```yaml
# docker-compose.local.yml
services:
  jupyter:
    build: {context: ., dockerfile: Dockerfile.singleuser}
    environment:
      HUGR_URL: "http://hugr:15000/ipc"
      HUGR_AUTH_MODE: "management"
      HUGR_SECRET_KEY: "local-dev-secret"
    ports: ["8888:8888"]
    volumes:
      - ./work:/home/jovyan/work
      - ../hugr-kernel:/opt/hugr-kernel-src:ro
    networks: [hub-network]

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

---

## Milestone 2: Hub Service

Go service built on `airport-go`. Provides hub data as Hugr data source, manages agents, runs system scheduler.

### Docker Compose Addition

```yaml
  hub-service:
    build: {context: ., dockerfile: Dockerfile.hub-service}
    environment:
      HUB_DB_DSN: "postgres://hub:hub@hub-db:5432/hub?sslmode=disable"
      HUGR_URL: "http://hugr:15000"
      HUGR_SECRET_KEY: "${HUGR_SECRET_KEY}"
      FLIGHT_BIND: ":50051"
      API_BIND: ":8080"
      DOCKER_HOST: "unix:///var/run/docker.sock"
      AGENT_IMAGE: "hugr-lab/hub-agent:latest"
      AGENT_NETWORK: "hub-network"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    ports: ["50051:50051", "8080:8080"]
    networks: [hub-network]

  hub-db:
    image: pgvector/pgvector:pg16
    environment:
      POSTGRES_DB: hub
      POSTGRES_USER: hub
      POSTGRES_PASSWORD: hub
    volumes: [hub-db-data:/var/lib/postgresql/data]
    networks: [hub-network]
```

Hugr config adds data source:

```yaml
data_sources:
  - name: hub
    type: airport
    dsn: "grpc://hub-service:50051"
```

### Agent Manager Interface

```go
type AgentManager interface {
    Ensure(ctx context.Context, userID string) (*Agent, error)
    Status(ctx context.Context, userID string) (*Agent, error)
    Stop(ctx context.Context, userID string) error
    List(ctx context.Context) ([]Agent, error)
}

type Backend interface {
    Create(ctx context.Context, spec AgentSpec) (string, error)
    Start(ctx context.Context, id string) error
    Stop(ctx context.Context, id string) error
    Remove(ctx context.Context, id string) error
    Status(ctx context.Context, id string) (AgentStatus, error)
}

type AgentSpec struct {
    UserID     string
    Image      string
    WorkVolume string
    Network    string
    Env        map[string]string
    Resources  ResourceLimits
    Mounts     []MountSpec  // subPath mounts for PVC isolation
}
```

Docker and K8s backends implement `Backend`. ~50-70 lines each per method.

---

## Milestone 3: Agent Runtime

### Agent Container

```text
┌─ alice-agent ──────────────────────────────────────────────────┐
│                                                                │
│  hub-agent process (Go)                                        │
│  ├── LLM Client (Claude API / configurable)                    │
│  │                                                             │
│  ├── MCP Client → Hub Service:8080/mcp/alice                   │
│  │   Hugr tools (proxied with management secret + identity):   │
│  │   ├── discovery-search_*                                    │
│  │   ├── schema-type_*                                         │
│  │   ├── data-inline_graphql_result                            │
│  │   ├── data-validate_graphql_query                           │
│  │   Hub tools (executed in Hub Service directly):              │
│  │   ├── hub.memory-store / search / forget                    │
│  │   ├── hub.registry-register / search                        │
│  │   ├── hub.dive-publish                                      │
│  │   └── hub.workspace-status                                  │
│  │                                                             │
│  └── Local MCP Server (in-process, no network)                 │
│      ├── python_exec      (Python in sandbox)                  │
│      ├── bash_exec        (shell in sandbox)                   │
│      ├── web_search       (internet search)                    │
│      ├── web_fetch        (download page)                      │
│      ├── file_read        (/workspace/)                        │
│      ├── file_write       (/workspace/)                        │
│      └── file_list        (/workspace/)                        │
│                                                                │
│  Auth: NONE. No credentials in container.                      │
│  All Hugr access goes through Hub Service.                     │
│                                                                │
│  Mounts (K8s):                                                 │
│  ├── PVC subPath: shared/ → /workspace/shared (rw)             │
│  └── PVC subPath: .agent/ → /workspace/.agent (rw)             │
│  notebooks/ NOT mounted — agent cannot access private files    │
└────────────────────────────────────────────────────────────────┘
```

### Skills System

Skills loaded from `/workspace/.agent/skills/`. Admin-curated, no public registry.

```markdown
---
name: data-analysis
description: Analyze datasets using Hugr GraphQL queries
tools: [discovery-search_modules, data-inline_graphql_result,
        hub.memory-store, file_write]
---

## Instructions
When the user asks to analyze data:
1. Check memory for cached schema info (hub.memory-search)
2. Use discovery tools to find data sources
3. Formulate and validate GraphQL queries
4. Execute queries, examine results
5. Store useful patterns in memory
6. Save results to /workspace/shared/results/
```

### Tool Policy

```json
{
  "default": {
    "allow": ["discovery-*", "schema-*", "data-*", "hub.memory-*",
              "hub.registry-*", "file_read", "file_list", "file_write",
              "python_exec", "web_search"],
    "deny": ["file_write:*.env", "file_write:*.key", "bash_exec"]
  },
  "power_user": {
    "allow": ["*"],
    "deny": ["file_write:*.env"]
  }
}
```

### Memory System

**Layer 1 — Local files** (fast, always available):

```text
/workspace/.agent/memory/
├── schema/                    # cached schema structure
├── queries/                   # reusable query patterns
├── context/                   # user preferences, project notes
└── MEMORY.md                  # index
```

**Layer 2 — Hugr vector store** (semantic search):
Agent calls `hub.memory-store` / `hub.memory-search` via Hub Service MCP → Hub DB (pgvector).

**Layer 3 — Query registry** (shared):
`hub.registry.queries` — public entries visible to all, private to author.

---

## Milestone 4: Dives

### Concept

A Dive is a self-contained React app with embedded GraphQL queries. Online Dives execute queries under the **viewer's** identity.

### Two Modes

**Offline**: Static JSON snapshot embedded at build time. No auth needed, safe to share publicly.

**Online**: Viewer opens Dive URL → React app calls Dive proxy → proxy queries Hugr with viewer's OIDC token (from JupyterHub session) → Hugr applies viewer's RBAC.

### Dive Structure

```text
~/work/shared/dives/sales-overview/
├── package.json
├── dive.json           # manifest: queries, params, permissions
├── index.tsx           # main React component
├── components/
└── queries/
    ├── revenue.graphql
    └── regions.graphql
```

### Security

- Online queries execute under **viewer's** token, never author's
- Dive code sandboxed in iframe with CSP
- Only queries declared in dive.json can execute
- Visibility: private / team / org

---

## Milestone 5: Scheduler

### User-Level (in workspace container)

- Lightweight cron (APScheduler / Tornado IOLoop)
- Tasks: refresh dive data, run notebook, execute query
- Runs while workspace container is alive
- Stored in `hub.scheduler.tasks` with `scope: "user"`

### System-Level (Hub Service)

- Built-in cron or Airflow integration
- Tasks: nightly reports, cross-user ETL, monitoring
- Runs even when user is offline
- Uses management secret + user identity → Hugr RBAC
- No token expiry problem — management secret is permanent
- Stored in `hub.scheduler.tasks` with `scope: "system"`

---

## Alternatives Considered

### OIDC tokens for agent (rejected)

Pass OIDC refresh tokens to agent containers. Rejected:
- Credentials in agent container (security risk)
- Token refresh chain breaks when user is offline
- Complex token lifecycle management
- Management secret + identity headers achieves same RBAC without any of these problems

### Hub metadata in separate DB with custom API (rejected)

Store memory, registry in separate PostgreSQL with REST API. Rejected:
- Duplicates Hugr infrastructure
- Loses RBAC, vector search, GraphQL
- Airport-go gives us all of this for free

### Agent as goroutine in Hub Service (rejected)

Run agents in Hub Service process. Rejected:
- Agent needs Python execution, bash, web search — requires sandbox
- Running arbitrary code in Hub Service is a security risk

### Agent as sidecar in workspace Pod (rejected for K8s)

Put agent as second container in workspace Pod. Rejected:
- Different lifecycle (agent may outlive workspace)
- Different resource limits
- Want independent management
- PVC subPath achieves file sharing without coupling

### OpenClaw as agent framework (rejected)

Rejected: CVE-2026-25253, public skill registry compromise. Adopted markdown skill format pattern only.

---

## Risks & Open Questions

### Risks

1. **Management secret scope**: One secret grants identity impersonation for any user. Mitigation: secret only in Hub Service, audit logging, rotate periodically.
2. **Agent prompt injection**: LLM manipulated to call unintended tools. Mitigation: tool policy engine, no credentials in agent, audit logging.
3. **Dive code injection**: Malicious React code. Mitigation: iframe sandbox, CSP headers.
4. **PVC subPath isolation**: Depends on container runtime enforcement. Verify with security.

### Open Questions

1. **OIDC client**: Create separate `hugr-hub` confidential client? Recommendation: yes.
2. **Dive bundler**: Vite (recommended).
3. **Agent LLM**: Claude API default, Ollama for air-gapped. Configurable.
4. **Embedding model**: Use Hugr's existing embedder (`management.mcp.embedderURL`).
5. **Hub Service binary name**: `hub-service`? `hugr-hub`?
6. **Agent chat protocol**: WebSocket from JupyterLab extension → Hub Service → Agent? Or direct to agent?

---

## Implementation Roadmap

```text
M1: JupyterHub + OIDC + Kernels              Weeks 1-3
    ├── Dockerfile.hub, jupyterhub_config.py
    ├── Dockerfile.singleuser (kernels + extensions)
    ├── docker-compose.yml (Hub + Hugr + Keycloak)
    ├── docker-compose.local.yml (no OIDC, management auth)
    ├── hugr-kernel: hub mode + management auth
    └── E2E: login → spawn → query → token refresh

M2: Hub Service                                Weeks 4-7
    ├── Go service: airport-go Flight server
    ├── Hub DB schema (pgvector migrations)
    ├── Flight catalog: memory, registry, dives, scheduler, workspace
    ├── MCP server endpoint (per-user, management auth to Hugr)
    ├── Agent Manager: Docker + K8s backends
    ├── Register in Hugr as Airport data source
    ├── post_auth_hook: receive user identity from JupyterHub
    └── E2E: hub.* queries via Hugr GraphQL

M3: Agent Runtime                              Weeks 8-12
    ├── hub-agent binary (Go): LLM client + MCP client + local tools
    ├── Agent container image (Python + hub-agent)
    ├── Agent Manager activated (spawn on user request)
    ├── Local tools: python_exec, bash, web_search, file ops
    ├── Skills engine (markdown SKILL.md)
    ├── Tool policy engine (allowlist + deny + audit)
    ├── Memory: local files + hub.memory vector search
    ├── JupyterLab chat extension
    └── PVC subPath isolation

M4: Dives                                      Weeks 13-16
    ├── hugr-dive-sdk (React library)
    ├── Dive builder (Vite in workspace)
    ├── Dive Registry in Hub Service
    ├── Viewer-identity query proxy
    └── Agent skill: dive creation

M5: Scheduler                                  Weeks 17-19
    ├── User-level: cron in workspace
    ├── System-level: cron in Hub Service (management auth)
    └── Task types: notebook, query, agent prompt, dive refresh
```
