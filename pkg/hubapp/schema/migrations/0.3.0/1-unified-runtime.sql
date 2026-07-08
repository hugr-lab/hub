-- Migration 003: Unified Runtime (Spec G)
-- Renames skills→allowed_skills, adds runtime_context on agent_types,
-- creates agent_results table for cross-process result lookup.

-- Rename skills → allowed_skills for clarity (skills was from Spec E, never had data)
ALTER TABLE agent_types
  RENAME COLUMN skills TO allowed_skills;

-- Runtime context filter: "any" (default), "local" (workspace only), "remote" (container only)
ALTER TABLE agent_types
  ADD COLUMN IF NOT EXISTS runtime_context TEXT DEFAULT 'any';

-- Result store metadata — mirrors what agents write to disk.
-- Enables cross-process queries like "does this result already exist".
CREATE TABLE IF NOT EXISTS agent_results (
  name TEXT NOT NULL,
  agent_id TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  conversation_id TEXT,
  total_rows INT,
  total_bytes BIGINT,
  schema JSONB,
  pinned BOOLEAN DEFAULT false,
  created_at TIMESTAMPTZ DEFAULT now(),
  PRIMARY KEY (agent_id, name)
);

CREATE INDEX IF NOT EXISTS idx_agent_results_conv ON agent_results(conversation_id);
