-- 0.3.1 → 0.3.2 (HB6 store-prune landing): add the platform interaction tables
-- projects + chats. These were added to init.sql when HB6a pruned the ADK
-- transcript tables, but a DB provisioned at 0.3.1 (before the prune) never got
-- them because the _hugr_app_meta version already matched appVersion and init
-- was skipped. This migration lands them on an existing platform DB.
--
-- Idempotent (CREATE TABLE/INDEX IF NOT EXISTS): safe on a DB that already has
-- them and never touches the existing users/user_agents/agents/budgets rows.

-- Projects — user-owned grouping of chats.
CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY,
  owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now(),
  updated_at TIMESTAMPTZ DEFAULT now()
);

-- Chats — the user-facing conversation thread. `agent_id` / `root_session_id`
-- are LOGICAL references into the Agent DB (hub.agent.db), not physical FKs; the
-- cross-source relation fields are declared by the HB-EXT extension source.
CREATE TABLE IF NOT EXISTS chats (
  id TEXT PRIMARY KEY,
  project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT 'New Chat',
  root_session_id TEXT,
  created_at TIMESTAMPTZ DEFAULT now(),
  updated_at TIMESTAMPTZ DEFAULT now(),
  last_active_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_projects_owner ON projects(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_chats_user ON chats(user_id);
CREATE INDEX IF NOT EXISTS idx_chats_agent ON chats(agent_id);
CREATE INDEX IF NOT EXISTS idx_chats_project ON chats(project_id);
