# hub-chat: REST → GraphQL migration

Parallel to `extensions/hub-admin/MIGRATION.md`. Completes Spec A+ GraphQL API
(`design/007-agent-platform-v2/spec-a-plus-graphql-api.md`).

## Status

| Surface | Backend | Where |
|---|---|---|
| Conversation list / CRUD | GraphQL | `convApiGraphQL.ts` |
| Messages query (with ownership) | GraphQL | `convApiGraphQL.ts` |
| Branch / summarize conversation | GraphQL (struct returns) | `convApiGraphQL.ts` |
| LLM model list | GraphQL | `convApiGraphQL.ts` |
| Agent instances (sidebar selector) | GraphQL | `convApiGraphQL.ts` |
| Chat streaming (token / thinking / tool_call / tool_result / summary) | **WebSocket (unchanged)** | `api.ts` |

`api.ts` now contains only the WebSocket transport plus type re-exports from
`convApiGraphQL.ts`. The Python proxies `ConversationAPIHandler`,
`AgentInstancesAPIHandler`, `ModelsAPIHandler` have been removed from
`hub_chat/app.py` — only `ChatWebSocketHandler` + `ChatConfigHandler` remain.

## Architecture

```
ChatSidebar.ts / ChatDocument.ts
   │
   ▼
convApiGraphQL.ts
   │
   ▼
hugr_connection_service                ← Python proxy adds OIDC bearer
   │
   ▼
Hugr query-engine
   │
   ├─→ standard insert/update/delete on hub.db.* (not currently used —
   │   every write path below goes through an airport-go mutating function
   │   so ownership checks and identity injection are centralized)
   │
   └─→ airport-go functions registered in pkg/hubapp/:
       ├── READ  (table functions, ArgFromContext user_id)
       │   ├── my_conversations(folder?, limit?)
       │   ├── my_conversation_messages(conversation_id, limit?, before?)
       │   └── my_agent_instances
       └── WRITE (mutating functions, ArgFromContext user_id)
           ├── create_conversation(input: create_conversation_input!)   → conversation_handle
           ├── rename_conversation(id, title)                           → String
           ├── delete_conversation(id)                                  → String
           ├── move_conversation(id, folder)                            → String
           ├── branch_conversation(input: branch_conversation_input!)   → conversation_handle
           └── summarize_conversation(conversation_id, up_to_message_id)→ summarize_result
```

## Function map (REST → GraphQL)

| Old REST helper (deleted) | New GraphQL helper | Backend |
|---|---|---|
| `convAPI('create', {mode, title, agent_id, model})` | `createConversation({mode, title?, agent_id?, model?})` | `mutation { function { hub { create_conversation(input: ...) { id title mode parent_id branch_point_message_id } } } }` |
| `convAPI('list')` | `listConversations(folder?, limit?)` | `query { hub { my_conversations(folder, limit) { ... } } }` |
| `convAPI('messages', {id, limit, before})` | `loadMessages(cid, limit?, before?)` | `query { hub { my_conversation_messages(conversation_id, limit, before) { ... } } }` |
| `convAPI('rename', {id, title})` | `renameConversation(id, title)` | `mutation { function { hub { rename_conversation(id, title) } } }` |
| `convAPI('delete', {id})` | `deleteConversation(id)` | `mutation { function { hub { delete_conversation(id) } } }` (soft delete) |
| `convAPI('move', {id, folder})` | `moveConversation(id, folder)` | `mutation { function { hub { move_conversation(id, folder) } } }` |
| `convAPI('branch', {...})` returning `{id, parent_id, title}` | `branchConversation({parent_id, branch_point_message_id, title?, branch_label?})` returning `conversation_handle` | `mutation { function { hub { branch_conversation(input: ...) { ... } } } }` |
| `convAPI('summarize', {...})` returning `{summary_id, summary, messages_summarized}` | `summarizeMessages(cid, mid)` returning `summarize_result` | `mutation { function { hub { summarize_conversation(conversation_id, up_to_message_id) { id summary_text message_count } } } }` |
| `GET /hub-chat/api/models` | `listModels()` | `query { function { core { models { model_sources { ... } } } } }` (cached) |
| `GET /hub-chat/api/agent/instances` | `listAgentInstances()` | `query { hub { my_agent_instances { ... } } }` |

## Bug fix captured by this migration

The old REST handler `handleConversationSummarize` (`pkg/hubapp/conversations.go`)
still referenced the `agent_messages.summary_of TEXT[]` column, which was
replaced by the `hub.db.message_summary_items` junction table in the Spec A+
schema migration. The REST path had been broken since the migration landed;
the only working summarization path in the meantime was the airport-go
`summarize_conversation` function. Deleting `conversations.go` alongside the
REST surface removes the broken handler from the codebase.

The airport-go `handleSummarizeConversation`
(`pkg/hubapp/handlers_conversation.go`) uses the junction table correctly via
a single atomic nested insert:

```graphql
mutation {
  hub { db {
    insert_agent_messages(data: {
      id: $id, conversation_id: $cid, role: "system", content: $content,
      is_summary: true, summary_items: $items
    }) { id }
    update_agent_messages(filter: { id: { in: $ids } }, data: { summarized_by: $id }) { affected_rows }
  } }
}
```

## Identity propagation

Same mechanism as `hub-admin/MIGRATION.md`. All mutating and table functions
declare hidden args via `app.ArgFromContext`; the Hugr planner substitutes them
from `[$auth.user_id | user_name | role | auth_type]` placeholders at request
time. The frontend never sees or passes them.

```
JupyterLab (OIDC token in cookies)
  → hugr_connection_service (adds Authorization: Bearer <token>)
  → Hugr (validates JWT, builds AuthInfo in context)
  → planner (substitutes [$auth.*] placeholders)
  → airport-go handler in pkg/hubapp/handlers_*.go
  → verifyConversationOwner / checkAgentAccess — ownership enforcement
  → a.client.Query(withIdentity(ctx, u), ...) — nested Hugr calls run as the user
```

## What's NOT migrating

These remain as WebSocket / protocol transport — GraphQL is not the right tool:

- `/hub-chat/ws/{conversation_id}` → hub-service `/ws/{conversation_id}` (chat streaming)
- `/hub-chat/api/config` — tiny helper that returns the WebSocket base URL
- MCP JSON-RPC (`/mcp/` on hub-service)
- OpenAI-compatible chat completions (`/v1/*` on hub-service) for third-party clients
- Agent container WebSocket uplink (`/agent/ws/{instance_id}` on hub-service)

## v0.3.24 struct returns in use

- `create_conversation` and `branch_conversation` return a `conversation_handle`
  so the sidebar can render the new row without a follow-up `listConversations`.
- `summarize_conversation` returns `{id, summary_text, message_count}` so the
  chat document can update the UI without re-fetching.
- `start_agent` / `stop_agent` return `{id, status, container_id}` (used by
  hub-admin) — saves a round-trip to `hub.agent_runtime` after each action.
- `create_conversation` and `branch_conversation` accept `InputStruct` arguments
  (`create_conversation_input`, `branch_conversation_input`) with nullable
  fields — no more empty-string sentinels for optional args.
