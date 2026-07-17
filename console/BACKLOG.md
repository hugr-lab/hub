# Hub console — backlog & open design

Captured 2026-07-17 from live-dogfood feedback. Repo tags: `[hugen]` = agent
runtime image, `[hub]` = hub-service (F5), `[console]` = SPA.

## Bugs / status model (highest impact — blocks confident testing)

- **Orphaned "active" sessions after a container restart.** Recreating the agent
  container kills in-flight turns; the persisted sessions stay `status=active`
  forever (confirmed: 9 stuck roots/subagents from before a restart). They never
  resume and can't be told apart from live ones → every old chat shows "working".
  Fix: on boot, reconcile sessions that were `active` at shutdown →
  `terminated`/`interrupted` (or attempt resume). `[hugen]`
- **Status model for async missions.** Root defaults to `wait=async`: it replies
  and its own turn goes idle while the mission subtree runs. A chat should read
  "working" when ANY descendant is active, and idle only when the whole tree is.
  Today the pill follows the root's own lifecycle_state, which is misleading.
  Derive chat status from the liveview `children` tree (any active → working).
  `[console]` (+ maybe a rolled-up state in the liveview payload `[hugen]`)
- **Stopping story is unclear** — what stop/cancel actually does to a running
  mission vs the container needs to be nailed down + surfaced. `[hugen/hub]`

## Persistence

- **Container restart wipes the agent's work** (artifacts, session files) because
  they live in the container, not a durable mount. Design: mount a per-agent host
  folder; auto-create a per-session subfolder on start (if absent). Two env vars:
  one for hub-service (the base dir where it CREATES per-agent folders) and one
  passed INTO the container (the mount target the agent writes to). Today the path
  is an ad-hoc local symlink. `[hub]` (+ Dockerfile mount `[hugen]`)

## Chat UX

- **Lazy history.** Don't replay the whole event log on open — render only the
  recent tail + reverse-scroll with dynamic older-page loading (and/or a local
  store cache). `[console]` (+ paged `/events` already exists `[hugen]`)
- **Last-activity time in the chat list** — "1 minute ago", switch to an absolute
  date after ~24h. `[console]` (data: `my_chats.last_active_at`)
- **Close a session** from the UI. `[console]` + a close verb `[hub/hugen]`
- **Clear a session's context** (compact/reset) from the UI. `[console]` +
  `[hugen]` (compactor reset exists)

## Agents

- **Delete agents** from the UI, soft-delete via `deleted_at` (agent_types +
  agents already carry `deleted_at`). Verify `delete_agent` semantics (hard vs
  soft) and surface it. `[console]` + `[hub]`
- ✅ `orchestration.env` (per-type env at spawn) — DONE.
- ✅ Agent container logs in UI (`/api/v1/agents/{id}/logs`) — DONE.
- ✅ Log-level quick field (`orchestration.env.HUGEN_LOG_LEVEL`) — DONE.

## Scheduling / tasks

- **Notifications + schedules per session**; view a session's tasks
  (hub.agent.db.tasks / task_log). `[console]` + `[hugen]`
