# Implementation Stages

## Stage 1: JupyterHub + OIDC + User Workspace with Managed Hugr Connection

Core platform: users log in via OIDC, get isolated workspace with pre-configured Hugr connection. Tokens refreshed automatically. No Hub Service required.

**Detailed spec**: [stage-1-spec.md](stage-1-spec.md)

## Stage 2: Hub Service (airport-go)

Go service that registers as Hugr Airport data source. Provides hub.* schemas (memory, query registry, dives metadata, scheduler tasks, workspace management). Manages agent containers (Docker + K8s backends). Exposes per-user MCP endpoints for agents using management auth + identity headers.

**Requirements:**

- Go service built on `airport-go` library
- Hub DB: PostgreSQL + pgvector for all hub data
- Flight catalog: hub.memory, hub.registry, hub.dives, hub.scheduler, hub.workspace
- Agent Manager with `Backend` interface (Docker, K8s implementations)
- MCP server endpoint per user: `/mcp/{user_id}` — forwards Hugr tools with management secret + user identity headers, executes hub.* tools locally
- User identity store: populated from JupyterHub `post_auth_hook` (user_id, user_name, role, email)
- Workspace token management: takes over from JupyterHub API polling (pushes fresh access_tokens)
- Hugr registration: `data_sources: [{name: hub, type: airport, dsn: "grpc://hub-service:50051"}]`
- Admin API: list users, list agents, stop/start agents, update tool policies

## Stage 3: Agent Runtime

Isolated agent containers with sandboxed tool execution. Agent connects to Hub Service as MCP client (no credentials in agent container).

**Requirements:**

- `hub-agent` Go binary: LLM client + MCP client (to Hub Service) + local MCP server (python, bash, files, web)
- Agent container image: Python 3.12 + hub-agent + pandas/pyarrow/geopandas
- PVC subPath mounts: `shared/` (rw) + `.agent/` (rw), no access to `notebooks/`
- Skills engine: load SKILL.md files from `/workspace/.agent/skills/`, admin-curated only
- Tool policy engine: allowlist + deny rules per role, deny takes precedence, audit log
- Memory Layer 1: local markdown files in `/workspace/.agent/memory/`
- Memory Layer 2: hub.memory.* via Hub Service MCP (vector search in Hub DB/pgvector)
- Query registry: hub.registry.* via Hub Service MCP
- Chat API: WebSocket endpoint for JupyterLab extension
- JupyterLab chat extension: sidebar panel, connects to Hub Service, routes to user's agent

## Stage 4: Dives (Interactive Visualizations)

React-based data apps with viewer-identity query execution.

**Requirements:**

- `hugr-dive-sdk`: React library — `useHugrQuery()`, `<HugrChart>`, `<HugrMap>`, `<PerspectiveViewer>`
- Dive builder: Vite dev server, preview as JupyterLab extension (WebView panel)
- `dive.json` manifest: queries (inline or registry refs), parameters, permissions, visibility
- Dive Registry in Hub Service: publish, version, list, share
- Viewer proxy: serves built React app, proxies GraphQL queries to Hugr with viewer's OIDC token
- Offline mode: static JSON snapshot at build, no auth needed
- Online mode: queries via proxy with viewer's JupyterHub session cookie → OIDC token
- Agent skill: `dive-builder` — create dives from natural language prompts
- For complex/big data: future integration with Spark or Apache Sedona for spatial data

## Stage 5: Scheduler

Two-level task scheduling under user identity.

**Requirements:**

- User-level: lightweight cron in workspace container (APScheduler/Tornado IOLoop), runs while container alive
- System-level: cron in Hub Service, runs even when user offline, uses management secret + user identity
- Task types: notebook (papermill), GraphQL query, agent prompt, dive refresh
- Task registry: `hub.scheduler.tasks` (scope: user/system, schedule, type, config)
- Execution history: `hub.scheduler.task_runs` (status, duration, result/error)
- Token for system tasks: management secret + user identity (no expiry)
- Future: Airflow integration for complex ETL pipelines, Spark/Sedona for heavy spatial workloads
- Future: webhook notifications on task completion/failure (Slack, email)
