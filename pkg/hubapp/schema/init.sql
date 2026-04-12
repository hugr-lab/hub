-- Hub Service database schema
-- Template parameters (expanded by Hugr provisioner):
--   {{.VectorSize}} — embedding vector dimensions
--   {{.EmbedderName}} — configured embedding source name

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

-- Agent types (predefined configurations)
CREATE TABLE IF NOT EXISTS agent_types (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  description TEXT,
  image TEXT NOT NULL,
  capabilities TEXT[] DEFAULT '{}',
  skills TEXT[] DEFAULT '{}',
  tool_policy JSONB DEFAULT '{}',
  max_instances_per_user INT DEFAULT 1,
  idle_timeout_seconds INT DEFAULT 3600,
  metadata JSONB DEFAULT '{}'
);

-- Agents (identity only — runtime state lives in Hub Service memory)
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

-- User-Agent access (M:N with owner/member roles)
CREATE TABLE IF NOT EXISTS user_agents (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'member',
  created_at TIMESTAMPTZ DEFAULT now(),
  PRIMARY KEY (user_id, agent_id)
);

-- Conversations (persistent chat threads with threading support)
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT 'New Chat',
  folder TEXT,
  mode TEXT NOT NULL DEFAULT 'tools',
  agent_type_id TEXT REFERENCES agent_types(id),
  agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL,
  model TEXT,
  parent_id TEXT REFERENCES conversations(id) ON DELETE SET NULL,
  branch_point_message_id TEXT,
  branch_label TEXT,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT now(),
  updated_at TIMESTAMPTZ DEFAULT now()
);

-- Agent sessions
CREATE TABLE IF NOT EXISTS agent_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  agent_id TEXT REFERENCES agents(id) ON DELETE SET NULL,
  started_at TIMESTAMPTZ DEFAULT now(),
  ended_at TIMESTAMPTZ,
  metadata JSONB DEFAULT '{}'
);

-- Agent messages (with summarization + channel protocol support)
CREATE TABLE IF NOT EXISTS agent_messages (
  id TEXT PRIMARY KEY,
  session_id TEXT REFERENCES agent_sessions(id) ON DELETE SET NULL,
  conversation_id TEXT REFERENCES conversations(id) ON DELETE CASCADE,
  role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system', 'tool')),
  content TEXT NOT NULL,
  tool_calls JSONB,
  tool_call_id TEXT,
  tokens_used INT,
  model TEXT,
  summarized_by TEXT,
  is_summary BOOLEAN DEFAULT false,
  channel TEXT DEFAULT 'final',
  payload JSONB,
  token_count INT,
  model_used TEXT,
  context_snapshot_ref TEXT,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Junction table for message summarization: links a summary message to the originals it covers.
-- When is_summary=true on agent_messages, the originals are listed here.
-- Symmetric reverse: agent_messages.summarized_by points back to the summary.
CREATE TABLE IF NOT EXISTS message_summary_items (
  summary_message_id  TEXT NOT NULL REFERENCES agent_messages(id) ON DELETE CASCADE,
  original_message_id TEXT NOT NULL REFERENCES agent_messages(id) ON DELETE CASCADE,
  position INT NOT NULL,
  PRIMARY KEY (summary_message_id, original_message_id)
);

-- Agent memory (with vector search)
CREATE TABLE IF NOT EXISTS agent_memory (
  id TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  content TEXT NOT NULL,
  embedding VECTOR({{.VectorSize}}),
  category TEXT DEFAULT 'general',
  source TEXT,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Query registry
CREATE TABLE IF NOT EXISTS query_registry (
  id TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  query TEXT NOT NULL,
  description TEXT,
  tags TEXT[],
  usage_count INT DEFAULT 0,
  created_at TIMESTAMPTZ DEFAULT now(),
  updated_at TIMESTAMPTZ DEFAULT now()
);

-- Tool calls (audit)
CREATE TABLE IF NOT EXISTS tool_calls (
  id TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id TEXT REFERENCES agent_sessions(id) ON DELETE SET NULL,
  user_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  arguments JSONB,
  result_summary TEXT,
  duration_ms INT,
  tokens_in INT,
  tokens_out INT,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Agent runs (one row per conversation turn, lightweight index — payloads on disk)
CREATE TABLE IF NOT EXISTS agent_runs (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  turn_index INT NOT NULL,
  status TEXT NOT NULL DEFAULT 'running',
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  tokens_in INT DEFAULT 0,
  tokens_out INT DEFAULT 0,
  model_breakdown JSONB,
  context_ref TEXT,
  error TEXT,
  created_at TIMESTAMPTZ DEFAULT now()
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

-- LLM usage tracking (provider_id references Hugr data source name, no FK)
CREATE TABLE IF NOT EXISTS llm_usage (
  id TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT NOT NULL,
  provider_id TEXT NOT NULL,
  session_id TEXT REFERENCES agent_sessions(id) ON DELETE SET NULL,
  tokens_in INT NOT NULL,
  tokens_out INT NOT NULL,
  duration_ms INT,
  period_key TEXT NOT NULL,
  intent TEXT,
  resolved_model TEXT,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_conversations_user ON conversations(user_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_conversations_parent ON conversations(parent_id);
CREATE INDEX IF NOT EXISTS idx_agent_messages_conv ON agent_messages(conversation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_summarized ON agent_messages(summarized_by);
CREATE INDEX IF NOT EXISTS idx_msi_original ON message_summary_items(original_message_id);
CREATE INDEX IF NOT EXISTS idx_user_agents_user ON user_agents(user_id);
CREATE INDEX IF NOT EXISTS idx_user_agents_agent ON user_agents(agent_id);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_user ON agent_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_agent_memory_user ON agent_memory(user_id);
CREATE INDEX IF NOT EXISTS idx_agent_memory_category ON agent_memory(user_id, category);
CREATE INDEX IF NOT EXISTS idx_query_registry_user ON query_registry(user_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id);
CREATE INDEX IF NOT EXISTS idx_agent_runs_conv ON agent_runs(conversation_id);
CREATE INDEX IF NOT EXISTS idx_agent_runs_status ON agent_runs(status) WHERE status = 'running';
CREATE INDEX IF NOT EXISTS idx_llm_usage_period ON llm_usage(user_id, provider_id, period_key);
