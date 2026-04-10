# pkg/hubapp — Hub Service application layer

The Hub Service is a Hugr App: a Go process that registers itself with Hugr as
an [airport-go](https://github.com/hugr-lab/airport-go) data source and exposes
hub-specific functionality (agent runtime, conversation actions, memory, query
registry) through Hugr GraphQL.

This package contains everything that lives behind that registration.

## What hub-service exposes through Hugr

| Surface | Mechanism | Where |
|---|---|---|
| `hub.db.*` tables (agents, conversations, messages, memory, …) | Hugr standard CRUD on PostgreSQL | `schema/init.sql`, `schema/hub.graphql` |
| `hub.agent_runtime` (read live container state) | airport-go **table function** | `catalog.go` `registerAgentRuntime` |
| `hub.memory_search`, `hub.registry_search` | airport-go **table functions** | `catalog.go` `registerSearchFunctions` |
| Mutating functions: `start_agent`, `stop_agent`, `delete_agent` | airport-go **scalar mutating functions** | `handlers_agent.go` |
| Mutating functions: `branch_conversation`, `summarize_conversation` | airport-go **scalar mutating functions** | `handlers_conversation.go` |
| `conversation_context` recursive view (branched conv parent walk) | Hugr SDL `@view` with `@args` | `schema/hub.graphql` |

The full design is in `design/007-agent-platform-v2/spec-a-plus-graphql-api.md`.

## What hub-service exposes through plain HTTP

These are intentionally not GraphQL — they are protocol or transport endpoints:

| Endpoint | Why |
|---|---|
| `/health` | Kubernetes / Docker healthcheck |
| `/api/user/login` | OIDC token exchange |
| `/ws/{conversation_id}` | Chat WebSocket — token streaming, tool calls, etc. |
| `/agent/ws/{instance_id}` | Agent ↔ Hub Service WebSocket — agent connection |
| `/mcp/{user_id}` | MCP JSON-RPC tool protocol (used by agents) |
| `/v1/*` | OpenAI-compatible chat completions for third-party clients |

Plus, **temporarily**, the legacy REST handlers in `agents.go` and
`conversations.go` (start/stop/create/branch/summarize/...). These are
**deprecated** — they exist only while hub-admin and hub-chat extensions
migrate to GraphQL. They will be deleted once frontend migration is complete.

## Code layout

```
pkg/hubapp/
├── app.go                       — HubApp implements app.Application; Init wires everything together
├── config.go                    — env-driven Config struct
├── catalog.go                   — registerCatalog() aggregator + read functions
├── hugrclient.go                — Hugr client factory (WithSecretKeyAuth + subscription pool)
├── handlers_helpers.go          — userFromArgs, requireAdmin, requireUser, withIdentity, checkAgentAccess
├── handlers_agent.go            — start_agent, stop_agent, delete_agent + lookupAgentIdentity
├── handlers_conversation.go     — branch_conversation, summarize_conversation
├── conversations.go             — REST handlers (DEPRECATED) + helpers verifyConversationOwner, getConversationDepth
├── agents.go                    — REST handlers (DEPRECATED): create/start/stop/delete/rename + handleAgentInstances
├── schema/init.sql              — PostgreSQL schema (agents, user_agents, conversations, message_summary_items, …)
├── schema/hub.graphql           — Hugr SDL definitions for the schema above
└── schema/migrations/001_*.sql  — migration from spec 005 to A+
```

## Adding a new mutating function

A typical mutating function does three things:

1. Read identity from auto-injected hidden args
2. Check permissions (admin or resource access)
3. Run server-side logic (Docker, LLM, internal Hugr query, …) and return a scalar result

Example:

```go
// in handlers_agent.go (or a new handlers_*.go file)

func (a *HubApp) registerMyMutations() error {
    return a.mux.HandleFunc("default", "do_something", a.handleDoSomething,
        // Public arg the client passes:
        app.Arg("target_id", app.String),

        // Hidden identity args injected by the Hugr planner from auth context.
        // The client never sees these in the GraphQL schema.
        app.ArgFromContext("user_id",   app.String, app.AuthUserID),
        app.ArgFromContext("user_name", app.String, app.AuthUserName),
        app.ArgFromContext("role",      app.String, app.AuthRole),
        app.ArgFromContext("auth_type", app.String, app.AuthType),

        app.Return(app.String),
        app.Mutation(),  // → extend type MutationFunction
        app.Desc("Human-readable description shown in introspection."),
    )
}

func (a *HubApp) handleDoSomething(w *app.Result, r *app.Request) error {
    u := userFromArgs(r)
    if err := requireUser(u); err != nil {
        return err
    }

    targetID := r.String("target_id")
    if targetID == "" {
        return fmt.Errorf("target_id is required")
    }

    // ctx propagates identity to internal a.client.Query() calls so Hugr RBAC
    // applies as the caller, not as the secret-key principal.
    ctx := withIdentity(r.Context(), u)

    if err := a.checkAgentAccess(ctx, u, targetID, "owner"); err != nil {
        return err
    }

    // … server-side logic …

    a.logger.Info("did something", "target", targetID, "by", u.ID)
    return w.Set("ok")
}
```

Then call `a.registerMyMutations()` from `registerCatalog()` in `catalog.go`.

The function will be available as:

```graphql
mutation {
  function {
    hub {
      do_something(target_id: "X")  # hidden args auto-injected
    }
  }
}
```

## Permission model

Two layers:

1. **Hugr RBAC** (declared in `core.role_permissions`) — enforced *before* the
   handler runs. Use this to block entire mutation functions for non-admin roles.
2. **Resource-level checks** in handlers — `checkAgentAccess` looks up the
   `user_agents` table to verify the caller has at least the requested role on
   the target agent. `requireAdmin` allows only admin role / management /
   apiKey auth types.

The default identity for unauthenticated server-internal calls is `api`/admin
(secret-key principal). Tests and curl-with-secret-key behave as admin unless
`X-Hugr-User-Id` impersonation headers are sent.

## Identity propagation through the stack

```
Frontend (workspace OIDC token)
   │
   ▼
hugr_connection_service proxy (adds bearer)
   │
   ▼
Hugr planner: validates JWT, resolves perm.AuthVars(ctx)
   │  substitutes [$auth.user_id|user_name|role|auth_type] for hidden args
   ▼
airport-go DoExchange to hub-service (gRPC Flight, identity in arrow batch column)
   │
   ▼
handlerScalarFunc.Execute → Request.args[name] = injected value
   │
   ▼
Handler:  u := userFromArgs(r)         // reconstructs auth.UserInfo
          ctx := withIdentity(r.Context(), u)
          a.client.Query(ctx, …)        // internal Hugr call runs as user u
```

The handler does **not** receive the identity through `r.Context()` (Hugr
planner does not propagate user identity through Flight metadata) — it comes
through the hidden function arguments. This is intentional: it makes identity
propagation an explicit contract of each registered function and lets the SDL
generator hide them from the public schema.

## Internal Hugr calls from handlers

Handlers that need to read or write `hub.db.*` data use `a.client.Query(ctx, …)`.
Direct DB access is **not allowed** — see `feedback_no_direct_db.md` in the
team memory. The reason: hub.db is a Hugr-managed PostgreSQL instance, and
bypassing Hugr would skip RBAC, row-level filters, and schema management.

To make these internal calls run *as the caller* (so RBAC applies correctly),
wrap the context with `withIdentity(ctx, u)` first:

```go
ctx := withIdentity(r.Context(), u)
res, err := a.client.Query(ctx, gql, vars)
```

`withIdentity` is a thin wrapper around `client.AsUser(ctx, u.ID, u.Name, u.Role)`
which the query-engine client uses to set impersonation headers on every Query
or Subscribe call.

## Atomic multi-step operations

Hugr supports multiple root mutations in one request, executed as a single
PostgreSQL transaction. Use this whenever a function needs to modify multiple
tables atomically.

`summarize_conversation` is a good example:

```go
mRes, err := a.client.Query(ctx,
    `mutation($id: String!, $cid: String!, $content: String!,
              $items: [hub_db_message_summary_items_mut_input_data!]!,
              $ids: [String!]!) {
        hub { db {
            insert_agent_messages(data: {
                id: $id, conversation_id: $cid, role: "system",
                content: $content, is_summary: true,
                summary_items: $items                       # nested insert via @field_references
            }) { id }
            update_agent_messages(
                filter: { id: { in: $ids } }
                data: { summarized_by: $id }
            ) { affected_rows }
        } }
    }`,
    map[string]any{...},
)
```

Two operations, one transaction. The nested `summary_items` field is enabled
by the `@field_references` reverse declaration on `message_summary_items` —
Hugr automatically links the junction rows to the new message.

## Configuration

Driven by environment variables (see `config.go`):

| Variable | Default | Purpose |
|---|---|---|
| `HUGR_URL` | `http://localhost:15004` | Hugr base URL (kernel IPC endpoint = `${HUGR_URL}/ipc`) |
| `HUGR_SECRET_KEY` | (empty) | Management secret for `WithSecretKeyAuth` |
| `HUB_SERVICE_LISTEN` | `:10000` | HTTP server (REST + WebSocket + MCP) |
| `HUB_SERVICE_FLIGHT` | `:10001` | gRPC Arrow Flight server (airport-go) |
| `HUB_SERVICE_INTERNAL_URL` | `http://hub-service:8082` | URL announced to agents in their env |
| `HUB_DATABASE_DSN` | `postgres://hugr:hugr_password@localhost:18032/hub` | hub.db data source |
| `HUB_REDIS_URL` | `redis://localhost:6379/0` | Rate limiting + cache |
| `HUB_STORAGE_PATH` | `/var/hub-storage` | Root for users/agents/shared volumes |
| `HUB_AGENT_NETWORK` | `hub-dev-network` | Docker network for agent containers |
| `HUGR_QUERY_TIMEOUT` | `5m` | Per-query timeout for Hugr client |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

## Running

```bash
# Build + run locally (uses go.work to pick up local query-engine)
go run ./cmd/hub-service/

# Or via docker compose
docker compose -f docker-compose.dev.yml up -d hub-service
```

Watch logs for the magic line:

```
hub app initialized — DB provisioned, starting services
HTTP server starting addr=:8082
application server started and registered with Hugr name=hub
```

Once you see all three, hub-service is ready and the mutating functions are
visible in Hugr at `_module_hub_mut_function`.
