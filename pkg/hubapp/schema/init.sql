-- Hub Service platform database schema.
--
-- HB6 store-prune (2026-07-06): the ADK-era transcript/memory tables were
-- removed — the agent's real store lives in the Agent DB (hugen-owned,
-- source `hub.agent.db`). This platform DB now holds ONLY the organization
-- of hub users' interaction: users, access grants, projects, chats, budgets,
-- and spawn secrets. `chats` is the user-facing thread; it REFERENCES agent
-- sessions in the Agent DB (logical cross-source relation via an extension
-- source), it does not duplicate the transcript.
--
-- `agents` / `agent_types` are kept for now as the legacy Docker-spawn
-- identity registry; they are the duplicate of the Agent DB canon and die in
-- HB4 when the spawn contract rewires agent identity onto `hub.agent.db`.

CREATE EXTENSION IF NOT EXISTS vector;

-- Users (synced from OIDC via JupyterHub)
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  email TEXT,
  hugr_role TEXT NOT NULL DEFAULT '',
  profile TEXT,
  last_login_at TIMESTAMPTZ DEFAULT now(),
  metadata JSONB DEFAULT '{}'
);

-- Agent types (predefined configurations). LEGACY — canon lives in the Agent
-- DB (hub.agent.db.agent_types); kept until HB4 spawn rewire.
CREATE TABLE IF NOT EXISTS agent_types (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  description TEXT,
  image TEXT NOT NULL,
  capabilities TEXT[] DEFAULT '{}',
  allowed_skills TEXT[] DEFAULT '{}',
  tool_policy JSONB DEFAULT '{}',
  max_instances_per_user INT DEFAULT 1,
  idle_timeout_seconds INT DEFAULT 3600,
  runtime_context TEXT DEFAULT 'any',
  metadata JSONB DEFAULT '{}'
);

-- Agents (identity registry for the legacy Docker-spawn path). LEGACY — canon
-- lives in the Agent DB (hub.agent.db.agents); kept until HB4 spawn rewire.
CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  agent_type_id TEXT NOT NULL REFERENCES agent_types(id),
  display_name TEXT NOT NULL,
  description TEXT,
  hugr_user_id TEXT NOT NULL,
  hugr_user_name TEXT NOT NULL,
  hugr_role TEXT NOT NULL DEFAULT 'agent',
  last_activity_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT now(),
  metadata JSONB DEFAULT '{}'
);

-- User-Agent access (M:N with owner/member roles) — who may talk to which agent.
CREATE TABLE IF NOT EXISTS user_agents (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'member',
  created_at TIMESTAMPTZ DEFAULT now(),
  PRIMARY KEY (user_id, agent_id)
);

-- Projects — user-owned grouping of chats. Artifacts attached to a project
-- (shared into agent session context) land here in a later phase.
CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY,
  owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now(),
  updated_at TIMESTAMPTZ DEFAULT now()
);

-- Chats — the user-facing conversation thread. A chat is 1—1 with a hugen
-- ROOT session and 1—N with its full session tree; `root_session_id` is a
-- LOGICAL reference into the Agent DB (hub.agent.db.sessions), NOT a physical
-- FK. `agent_id` is likewise a logical reference to the Agent DB agent. The
-- authoritative transcript lives in the Agent DB; the chat never copies it.
-- MVP: one user + one agent (group chats = N users × N agents come later).
CREATE TABLE IF NOT EXISTS chats (
  id TEXT PRIMARY KEY,
  project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT 'New Chat',
  root_session_id TEXT,
  created_at TIMESTAMPTZ DEFAULT now(),
  updated_at TIMESTAMPTZ DEFAULT now(),
  last_active_at TIMESTAMPTZ DEFAULT now(),
  archived BOOLEAN NOT NULL DEFAULT FALSE,
  last_read_seq INTEGER NOT NULL DEFAULT 0
);

-- LLM budgets (provider_id references Hugr data source name, no FK)
CREATE TABLE IF NOT EXISTS llm_budgets (
  id TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  scope TEXT NOT NULL,
  provider_id TEXT,
  period TEXT NOT NULL CHECK (period IN ('hour', 'day', 'month')),
  max_tokens_in BIGINT,
  max_tokens_out BIGINT,
  max_requests INT,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- One-shot agent bootstrap secrets (spec-hub-side §1.5). Only the sha256 hex
-- of the secret is stored; the plaintext is returned once at mint time and
-- goes into the container env (HUGR_ACCESS_TOKEN). Consumed on first redeem.
CREATE TABLE IF NOT EXISTS agent_bootstrap_secrets (
  id TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  agent_id TEXT NOT NULL,
  secret_hash TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_bootstrap_secret_hash ON agent_bootstrap_secrets(secret_hash);
CREATE INDEX IF NOT EXISTS idx_user_agents_user ON user_agents(user_id);
CREATE INDEX IF NOT EXISTS idx_user_agents_agent ON user_agents(agent_id);
CREATE INDEX IF NOT EXISTS idx_projects_owner ON projects(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_chats_user ON chats(user_id);
CREATE INDEX IF NOT EXISTS idx_chats_agent ON chats(agent_id);
CREATE INDEX IF NOT EXISTS idx_chats_project ON chats(project_id);
