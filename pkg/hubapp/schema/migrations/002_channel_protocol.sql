-- Migration 002: Channel protocol, agent runs, intent routing
-- Adds channel-typed message metadata, per-turn run tracking, and intent-based
-- LLM usage attribution. Part of Spec F (Agent Runtime Foundation).

-- Channel protocol columns on agent_messages
ALTER TABLE agent_messages
  ADD COLUMN IF NOT EXISTS channel TEXT DEFAULT 'final',
  ADD COLUMN IF NOT EXISTS payload JSONB,
  ADD COLUMN IF NOT EXISTS token_count INT,
  ADD COLUMN IF NOT EXISTS model_used TEXT,
  ADD COLUMN IF NOT EXISTS context_snapshot_ref TEXT;

-- Lightweight run index: one row per conversation turn.
-- Payloads stay on disk (context_ref is a filesystem path, not a FK).
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

CREATE INDEX IF NOT EXISTS idx_agent_runs_conv ON agent_runs(conversation_id);
CREATE INDEX IF NOT EXISTS idx_agent_runs_status ON agent_runs(status) WHERE status = 'running';

-- Intent routing attribution on LLM usage records
ALTER TABLE llm_usage
  ADD COLUMN IF NOT EXISTS intent TEXT,
  ADD COLUMN IF NOT EXISTS resolved_model TEXT;

-- Fix FK constraints from migration 001 that may not have been applied.
-- Ensures agent deletion cascades properly.
ALTER TABLE conversations
  DROP CONSTRAINT IF EXISTS conversations_agent_id_fkey;
ALTER TABLE conversations
  ADD CONSTRAINT conversations_agent_id_fkey
  FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE SET NULL;

ALTER TABLE agent_sessions
  DROP CONSTRAINT IF EXISTS agent_sessions_agent_id_fkey;
ALTER TABLE agent_sessions
  ADD CONSTRAINT agent_sessions_agent_id_fkey
  FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE SET NULL;
