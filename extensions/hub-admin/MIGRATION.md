# hub-admin: REST → GraphQL migration

This document records how the hub-admin extension moved from the legacy REST
proxy (`hub-admin/api/hub/...`) to direct Hugr GraphQL calls.

## Status

| Surface | Backend | Where |
|---|---|---|
| Data Sources / Catalogs / Models | GraphQL | `api.ts` |
| Budgets / Usage | GraphQL | `api.ts` |
| **Agents (CRUD + lifecycle)** | **GraphQL** | `agentApiGraphQL.ts` |

All admin operations go through Hugr GraphQL via `hugr_connection_service`.
The hub-service REST endpoints under `/api/agent/*`, `/api/conversations/*`,
`/api/user/*`, and `/api/models` have been **deleted**. The Python proxy
`hub_admin/hub_proxy.py` has also been removed.

## Architecture

```
AdminPanel.ts
   │
   ▼
agentApiGraphQL.ts                  ← all CRUD + lifecycle here
   │
   ▼
hugr_connection_service             ← Python proxy adds OIDC bearer
   │
   ▼
Hugr query-engine
   │
   ├─→ standard insert/update/delete on hub.db.agents + hub.db.user_agents
   └─→ airport-go mutating functions (start_agent / stop_agent / delete_agent)
        registered in pkg/hubapp/handlers_agent.go
```

`AdminPanel.ts` imports agent operations from `agentApiGraphQL.ts`. Other
sections (data sources, catalogs, models, budgets) still import from `api.ts`
which already used GraphQL — they didn't need any migration.

## Function map (REST → GraphQL)

| Old REST helper (deleted) | New GraphQL | Backend |
|---|---|---|
| `hubServiceAPI('api/agent/create', POST, ...)` | `createAgent({...})` | `mutation { hub { db { insert_agents(...) insert_user_agents(...) } } }` (one transaction) |
| `hubServiceAPI('api/agent/start', POST, ...)` | `startAgent(agentId)` | `mutation { function { hub { start_agent(agent_id: ...) } } }` |
| `hubServiceAPI('api/agent/stop', POST, ...)` | `stopAgent(agentId)` | `mutation { function { hub { stop_agent(agent_id: ...) } } }` |
| `hubServiceAPI('api/agent/delete', POST, ...)` | `deleteAgent(agentId)` | `mutation { function { hub { delete_agent(agent_id: ...) } } }` |
| `hubServiceAPI('api/agent/rename', POST, ...)` | `renameAgent(agentId, name)` | `mutation { hub { db { update_agents(filter:..., data:...) } } }` |
| `fetchAgents()` (was REST proxy) | `fetchAgents()` | two parallel queries: `hub.db.agents` + `hub.agent_runtime`, merged client-side |
| `hubServiceAPI('api/user/agents', GET)` | `fetchUserAgents()` | `hub.db.user_agents { agent { ... } }` enriched with `hub.agent_runtime` |

The `Agent` interface (identity + optional runtime fields) lives in
`agentApiGraphQL.ts` and is re-exported from `api.ts` for backward
compatibility with code that still imports it from there.

## Identity propagation

Mutating functions (`start_agent` / `stop_agent` / `delete_agent`) take hidden
context-injected arguments via airport-go's `ArgFromContext`. The Hugr planner
substitutes these from `[$auth.user_id|user_name|role|auth_type]` placeholders
based on the request's authenticated identity. Frontend never passes them
explicitly — they are filtered out of the public GraphQL schema.

This means the workspace user's OIDC identity flows automatically through:

```
JupyterLab UI (OIDC token in cookies)
  → hugr_connection_service (adds Authorization: Bearer <token>)
  → Hugr (validates JWT, builds AuthInfo in context)
  → planner (substitutes [$auth.*] placeholders)
  → airport-go handler in pkg/hubapp/handlers_agent.go
  → checkAgentAccess(ctx, u, agentID, "owner") — RBAC check on hub.db.user_agents
  → DockerRuntime.Start(ctx, identity)
```

## Cleanup completed

The REST handlers in `pkg/hubapp/agents.go`, `pkg/hubapp/conversations.go`,
and `pkg/hubapp/users.go` have been deleted. The corresponding route
registrations were removed from `pkg/hubapp/app.go`. The Python proxy
`extensions/hub-admin/hub_admin/hub_proxy.py` (and its registration in
`app.py`) has also been deleted. See `extensions/hub-chat/MIGRATION.md`
for the parallel hub-chat frontend migration that unblocked this cleanup.

## What's NOT migrating

These remain on REST/WebSocket forever — they are protocol or transport, not CRUD:

- Workspace OIDC token refresh (handled by JupyterHub)
- WebSocket chat streaming (`/ws/{conversation_id}` in hub-chat)
- WebSocket agent connection (`/agent/ws/{instance_id}`)
- MCP JSON-RPC tool protocol (`/mcp/{user_id}`)
- OpenAI-compatible chat completions (`/v1/*` for third-party clients)

## v0.3.24 struct returns

query-engine `v0.3.24` shipped `Struct()` / `InputStruct()` / `FieldNullable()`.
The hub-service mutations now use these where they remove a frontend
round-trip:

- `start_agent` / `stop_agent` return `agent_runtime_state { id, status, container_id }`
  — hub-admin can update the runtime column without re-querying
  `hub.agent_runtime`.
- Conversation mutations (`create_conversation`, `branch_conversation`) return a
  `conversation_handle` struct and accept `InputStruct` args with nullable
  fields — hub-chat doesn't pass empty-string sentinels anymore.

`agentApiGraphQL.ts` can be polished to consume the new struct returns directly
(currently it still does a `fetchAgents()` follow-up after start/stop for
parity with the pre-struct era). That's a small cosmetic improvement, not a
blocker.
