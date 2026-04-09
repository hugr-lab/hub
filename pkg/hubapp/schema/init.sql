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

-- Agent instances (running containers)
CREATE TABLE IF NOT EXISTS agent_instances (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  agent_type_id TEXT NOT NULL REFERENCES agent_types(id),
  container_id TEXT,
  auth_token TEXT,
  status TEXT DEFAULT 'creating',
  started_at TIMESTAMPTZ DEFAULT now(),
  last_activity_at TIMESTAMPTZ DEFAULT now(),
  metadata JSONB DEFAULT '{}'
);

-- Conversations (persistent chat threads)
CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  title TEXT NOT NULL DEFAULT 'New Chat',
  folder TEXT,
  mode TEXT NOT NULL DEFAULT 'tools',
  agent_instance_id TEXT REFERENCES agent_instances(id),
  model TEXT,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT now(),
  updated_at TIMESTAMPTZ DEFAULT now()
);

-- Agent sessions
CREATE TABLE IF NOT EXISTS agent_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  instance_id TEXT REFERENCES agent_instances(id),
  started_at TIMESTAMPTZ DEFAULT now(),
  ended_at TIMESTAMPTZ,
  metadata JSONB DEFAULT '{}'
);

-- Agent messages
CREATE TABLE IF NOT EXISTS agent_messages (
  id TEXT PRIMARY KEY,
  session_id TEXT REFERENCES agent_sessions(id),
  conversation_id TEXT REFERENCES conversations(id),
  role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system', 'tool')),
  content TEXT NOT NULL,
  tool_calls JSONB,
  tool_call_id TEXT,
  tokens_used INT,
  model TEXT,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Agent memory (with vector search)
CREATE TABLE IF NOT EXISTS agent_memory (
  id TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT NOT NULL REFERENCES users(id),
  content TEXT NOT NULL,
  embedding VECTOR({{.VectorSize}}),
  category TEXT DEFAULT 'general',
  source TEXT,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Query registry
CREATE TABLE IF NOT EXISTS query_registry (
  id TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT NOT NULL REFERENCES users(id),
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
  session_id TEXT REFERENCES agent_sessions(id),
  user_id TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  arguments JSONB,
  result_summary TEXT,
  duration_ms INT,
  tokens_in INT,
  tokens_out INT,
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
  session_id TEXT REFERENCES agent_sessions(id),
  tokens_in INT NOT NULL,
  tokens_out INT NOT NULL,
  duration_ms INT,
  period_key TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_conversations_user ON conversations(user_id, deleted_at);
CREATE INDEX IF NOT EXISTS idx_agent_messages_conv ON agent_messages(conversation_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_instances_user ON agent_instances(user_id);
CREATE INDEX IF NOT EXISTS idx_agent_sessions_user ON agent_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_agent_memory_user ON agent_memory(user_id);
CREATE INDEX IF NOT EXISTS idx_agent_memory_category ON agent_memory(user_id, category);
CREATE INDEX IF NOT EXISTS idx_query_registry_user ON query_registry(user_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_session ON tool_calls(session_id);
CREATE INDEX IF NOT EXISTS idx_llm_usage_period ON llm_usage(user_id, provider_id, period_key);
