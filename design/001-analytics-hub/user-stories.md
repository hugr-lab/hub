# User Stories

## Roles

| Role | Description |
| ---- | ----------- |
| **Admin** | Manages Hub deployment, users, agents, tool policies, Hugr connection |
| **User** | Works with notebooks, queries, agent assistant, dives |
| **Analyst** | Power user who creates ETL pipelines, shared queries, scheduled tasks |

---

## Admin Stories

### A1: Initial Setup

As an admin, I want to deploy the Hub with Docker Compose, configure OIDC provider and Hugr connection, so that users can start working.

**Acceptance criteria:**
- `docker compose up` starts JupyterHub + Hugr + Keycloak + Hub Service + Hub DB
- OIDC client configured in Keycloak (hugr-hub, confidential)
- Hub Service registered in Hugr as Airport data source
- Management secret shared between Hub Service and Hugr
- First user can log in and get a workspace

### A2: User Management

As an admin, I want to see all registered users, their workspace and agent status, so that I can monitor and manage the platform.

**Acceptance criteria:**
- Admin can query `hub.workspace.users` via Hugr GraphQL or admin UI
- See: user_id, role, last_login, workspace status, agent status
- Can stop/start any user's workspace or agent
- Can change user's role (reflected in Hugr RBAC for agent operations)

### A3: Agent Tool Policy

As an admin, I want to configure which tools are available to agents per role, so that I can enforce security boundaries.

**Acceptance criteria:**
- Tool policy defined in config file or Hub DB
- Allowlist + deny rules per role
- Deny rules take precedence
- Changes apply to running agents without restart
- Audit log of all tool calls accessible via `hub.workspace.audit_log`

### A4: Skill Management

As an admin, I want to manage the set of skills available to agents, so that agents only have approved capabilities.

**Acceptance criteria:**
- Skills are SKILL.md files in a shared volume or Hub DB
- Admin can add/remove/update skills
- No public skill registry — only admin-curated skills
- Skills can be assigned per role (analyst gets ETL skills, regular user does not)
- Skill changes reflected in agents on next session

### A5: Monitoring and Resource Limits

As an admin, I want to set resource limits (CPU, RAM, disk) per workspace and agent, and monitor usage, so that one user doesn't starve others.

**Acceptance criteria:**
- Resource limits configurable per role in Hub Service config
- Applied at Docker/K8s level (container resource limits, PVC quotas)
- Usage metrics exposed via `hub.workspace.metrics` or Prometheus endpoint
- Alerts when user approaches limits

### A6: Hugr Connection Management

As an admin, I want to connect the Hub to multiple Hugr instances or switch between them, so that users can work with different data environments.

**Acceptance criteria:**
- Hub Service config supports multiple Hugr endpoints
- Each endpoint has its own management secret
- Users can be assigned to specific Hugr instances
- Future: user can switch between Hugr instances in their workspace

### A7: Backup and Recovery

As an admin, I want to backup Hub DB (agent memories, query registry, dives, scheduled tasks) and user PVCs, so that I can recover from failures.

**Acceptance criteria:**
- Hub DB backup via standard PostgreSQL tools (pg_dump)
- PVC backup strategy documented (volume snapshots or rsync)
- Recovery procedure tested and documented

---

## User Stories

### U1: First Login

As a user, I want to log in once via SSO and immediately get a working environment with all kernels pre-configured, so that I don't waste time on setup.

**Acceptance criteria:**
- Click "Login" → OIDC provider login page → redirect back
- JupyterLab opens with three kernels available: Hugr (GraphQL), DuckDB (SQL), Python
- Hugr connection pre-configured and authenticated — first query works immediately
- No manual `:connect` or token copy-paste required

### U2: Notebook Work

As a user, I want to query Hugr data using GraphQL, SQL, and Python in the same notebook environment, so that I can explore data in the most natural way.

**Acceptance criteria:**
- Hugr kernel: write GraphQL, see results as interactive tables (Perspective viewer)
- DuckDB kernel: write SQL, see results as tables with geo/map support
- Python kernel: `from hugr import HugrClient; client = HugrClient()` works (token from env)
- Results shareable across kernels (via files in `~/work/shared/`)

### U3: Start Agent

As a user, I want to start my AI assistant and ask it to analyze data, so that I can get insights without writing queries manually.

**Acceptance criteria:**
- Click "Start Agent" in JupyterLab or run mutation `hub.workspace.start_agent`
- Chat panel opens in JupyterLab sidebar
- Ask: "What data sources are available?" → agent uses discovery tools → lists modules
- Ask: "Show me sales by region for last month" → agent writes query, executes, returns results
- Agent saves results to `~/work/shared/results/` — visible in file browser
- Agent remembers schema structure for next time (memory)

### U4: Agent Memory

As a user, I want my agent to remember what it learned about data schemas and my preferences, so that it gets smarter over time.

**Acceptance criteria:**
- Agent caches schema information after first exploration (hub.memory.store)
- Next time user asks about same data → agent finds it in memory (hub.memory.search)
- User can say "remember that I always want dates in European format" → stored as preference
- User can say "forget the old sales schema" → memory entry removed
- Memory persists across agent restarts (stored in Hub DB via hub.memory)

### U5: Query Registry

As a user, I want to save useful GraphQL queries with descriptions and find them later, so that I don't rewrite the same queries.

**Acceptance criteria:**
- Agent or user can save a query: `hub.registry.register(name, query, description, tags)`
- Search by name or semantic similarity: `hub.registry.search("sales by region")`
- Private by default, can be made public (`is_public: true`)
- Public queries visible to all users
- Queries can be referenced in Dives

### U6: Create a Dive (Interactive Visualization)

As a user, I want to create an interactive dashboard from my analysis and share it with colleagues, so that they can explore the data themselves.

**Acceptance criteria:**
- Agent or user creates a Dive in `~/work/shared/dives/<name>/`
- Local preview with hot reload (Vite dev server)
- Publish to Hub: `hub.dives.publish(name, title, bundle, visibility)`
- Get shareable URL: `https://hub.example.com/dives/<id>`
- Colleague opens URL, sees the dashboard with live data
- Data queries execute under **colleague's** OIDC token (their RBAC, not author's)
- Offline mode: snapshot data at publish time, no auth needed

### U7: Schedule a Task

As a user, I want to schedule my agent to run an analysis every Monday morning and save results, so that I have fresh reports waiting for me.

**Acceptance criteria:**
- Create task: `hub.scheduler.create_task(name: "weekly-sales", schedule: "0 8 * * 1", type: "agent", config: {prompt: "..."})`
- Task runs under user's identity (management auth with user's role)
- Results saved to `~/work/shared/results/weekly-sales/`
- Task history in `hub.scheduler.task_runs`
- User can see status, cancel, modify schedule

### U8: Work Offline / Agent Autonomous

As a user, I want my agent to continue working on a long-running analysis even after I close my browser, so that I don't need to keep my laptop open.

**Acceptance criteria:**
- Close browser → agent continues running (separate container)
- Agent completes task, saves results to shared volume
- Next time user opens JupyterLab → results are in `~/work/shared/results/`
- Agent can notify user when done (future: email, Slack)

---

## Analyst Stories (Power User)

### P1: Create ETL Pipeline

As an analyst, I want to create a data transformation pipeline that reads from source, transforms, and writes results to a Hugr table or file, so that I can automate data preparation.

**Acceptance criteria:**
- Write ETL logic in a notebook (Python kernel with hugr-client + pandas)
- Test interactively in JupyterLab
- Register as a scheduled task: `hub.scheduler.create_task(type: "notebook", config: {path: "notebooks/etl/sales-daily.ipynb"})`
- Pipeline runs on schedule under analyst's identity
- Hugr mutations execute with analyst's RBAC
- Results tracked in `hub.scheduler.task_runs` (success/failure, duration, output)
- Failed runs produce logs accessible via `hub.scheduler.task_runs.result`

### P2: Review and Merge ETL Pipeline

As an analyst, I want to review a colleague's ETL pipeline and approve it for production scheduling, so that we have quality control on data transformations.

**Acceptance criteria:**
- Colleague creates ETL notebook in their workspace
- Colleague submits it for review (copies to shared location or uses Git)
- Analyst reviews the notebook (opens in their JupyterLab, runs against test data)
- Analyst approves: registers as system-level scheduled task
  `hub.scheduler.create_task(scope: "system", ...)`
- System-level task runs even when both users are offline
- Task runs under the **original author's identity** (their RBAC)
- Analyst with admin role can also set task to run under a service role

### P3: Shared Query Library

As an analyst, I want to curate a library of approved GraphQL queries that other users and agents can reference, so that we have consistent data access patterns.

**Acceptance criteria:**
- Analyst creates queries and marks them public: `hub.registry.register(is_public: true)`
- Tags queries by domain: `tags: ["sales", "official", "v2"]`
- Other users find them: `hub.registry.search("official sales queries")`
- Agents use them: `hub.registry-search` tool finds approved patterns
- Dives reference them: `"ref": "registry:sales/revenue-by-region"`
- Analyst can update a query (new version), old version still accessible

### P4: Data Quality Monitoring

As an analyst, I want to schedule data quality checks and get alerted when thresholds are violated, so that I catch data issues early.

**Acceptance criteria:**
- Create a quality check notebook that queries Hugr and asserts conditions
- Schedule as system-level task (runs every hour)
- On failure: task_run.status = "failed", result contains assertion details
- Future: webhook notification on failure (Slack, email)
- Dashboard (Dive) showing quality check history

### P5: Cross-Source Analysis

As an analyst, I want to query data from multiple Hugr modules and DuckDB local data in the same analysis, so that I can combine external and local data.

**Acceptance criteria:**
- Hugr kernel: query `customer.orders` and `iot.sensor_data` in one GraphQL query
- DuckDB kernel: `SELECT * FROM hugr_scan('customer.orders') JOIN local_csv` (future: `:connect_hugr`)
- Python kernel: hugr-client fetches Hugr data → pandas join with local CSV
- Agent can combine data sources in a single analysis
- Results saved to shared workspace

### P6: Dive Templates

As an analyst, I want to create reusable Dive templates that other users can instantiate with different parameters, so that common visualizations are standardized.

**Acceptance criteria:**
- Analyst creates a Dive with parameterized queries (e.g., `{region}` variable)
- Publishes as template: `visibility: "template"`
- Other users or agents instantiate: provide parameters → get customized Dive
- Template queries reference the registry: `"ref": "registry:sales/revenue-by-region"`
- Agent skill "dive-builder" knows about templates and can suggest them

---

## Story Map (by Milestone)

```text
         M1              M2              M3              M4           M5
    JupyterHub +     Hub Service      Agent          Dives        Scheduler
    OIDC + Kernels

Admin:
    A1 Setup         A2 Users        A3 Policies    A6 Multi-    A5 Monitoring
                     A6 Hugr conn    A4 Skills      Hugr         A7 Backup

User:
    U1 Login         U5 Query reg    U3 Agent       U6 Dives     U7 Schedule
    U2 Notebooks                     U4 Memory                   U8 Autonomous
                                     U8 Agent bg

Analyst:
    U2 Notebooks     P3 Query lib    P5 Cross-src   P6 Templates P1 ETL
                                                                  P2 Review
                                                                  P4 Quality
```
