-- Migration: 005 → A+ (agents, streaming, threading, summarization)
-- Run against existing hub database with agent_instances table.

-- 1. Create agents table (identity only — no runtime state)
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

-- 2. Create user_agents M:N access table
CREATE TABLE IF NOT EXISTS user_agents (
  user_id TEXT NOT NULL REFERENCES users(id),
  agent_id TEXT NOT NULL REFERENCES agents(id),
  role TEXT NOT NULL DEFAULT 'member',
  created_at TIMESTAMPTZ DEFAULT now(),
  PRIMARY KEY (user_id, agent_id)
);

-- 3. Migrate identity data from agent_instances → agents
INSERT INTO agents (id, agent_type_id, display_name,
                    hugr_user_id, hugr_user_name, hugr_role,
                    last_activity_at, created_at, metadata)
SELECT id, agent_type_id,
       COALESCE(display_name, agent_type_id),
       user_id, user_id, 'agent',
       last_activity_at, started_at, metadata
FROM agent_instances
ON CONFLICT (id) DO NOTHING;

-- 4. Create owner access grants from agent_instances
INSERT INTO user_agents (user_id, agent_id, role)
SELECT user_id, id, 'owner' FROM agent_instances
ON CONFLICT (user_id, agent_id) DO NOTHING;

-- 5. Conversations: add agent_id, populate from agent_instance_id
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS agent_id TEXT REFERENCES agents(id);
UPDATE conversations SET agent_id = agent_instance_id WHERE agent_instance_id IS NOT NULL;
ALTER TABLE conversations DROP COLUMN IF EXISTS agent_instance_id;

-- 6. Conversations: add threading columns
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS parent_id TEXT REFERENCES conversations(id);
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS branch_point_message_id TEXT;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS branch_label TEXT;

-- 7. Messages: add summarization columns + junction table
ALTER TABLE agent_messages ADD COLUMN IF NOT EXISTS summarized_by TEXT;
ALTER TABLE agent_messages ADD COLUMN IF NOT EXISTS is_summary BOOLEAN DEFAULT false;

CREATE TABLE IF NOT EXISTS message_summary_items (
  summary_message_id  TEXT NOT NULL REFERENCES agent_messages(id) ON DELETE CASCADE,
  original_message_id TEXT NOT NULL REFERENCES agent_messages(id) ON DELETE CASCADE,
  position INT NOT NULL,
  PRIMARY KEY (summary_message_id, original_message_id)
);

-- 7a. Migrate existing summary_of TEXT[] arrays into junction table (if column exists)
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name = 'agent_messages' AND column_name = 'summary_of') THEN
    INSERT INTO message_summary_items (summary_message_id, original_message_id, position)
    SELECT m.id, original_id, ord
    FROM agent_messages m,
         LATERAL unnest(m.summary_of) WITH ORDINALITY AS u(original_id, ord)
    WHERE m.summary_of IS NOT NULL AND array_length(m.summary_of, 1) > 0
    ON CONFLICT (summary_message_id, original_message_id) DO NOTHING;

    ALTER TABLE agent_messages DROP COLUMN summary_of;
  END IF;
END $$;

-- 8. Agent sessions: update FK from agent_instances to agents
ALTER TABLE agent_sessions DROP CONSTRAINT IF EXISTS agent_sessions_instance_id_fkey;
ALTER TABLE agent_sessions RENAME COLUMN instance_id TO agent_id;
ALTER TABLE agent_sessions ADD CONSTRAINT agent_sessions_agent_id_fkey
  FOREIGN KEY (agent_id) REFERENCES agents(id);

-- 9. Drop old table
DROP TABLE IF EXISTS agent_instances;

-- 10. New indexes
CREATE INDEX IF NOT EXISTS idx_user_agents_user ON user_agents(user_id);
CREATE INDEX IF NOT EXISTS idx_user_agents_agent ON user_agents(agent_id);
CREATE INDEX IF NOT EXISTS idx_conversations_parent ON conversations(parent_id);
CREATE INDEX IF NOT EXISTS idx_messages_summarized ON agent_messages(summarized_by);
CREATE INDEX IF NOT EXISTS idx_msi_original ON message_summary_items(original_message_id);

-- 11. Switch FK constraints to ON DELETE CASCADE / SET NULL so user/agent removal
--    cleans up dependents instead of failing silently with affected_rows = 0.

-- user_agents: cascade both ways (no orphan grants)
ALTER TABLE user_agents
  DROP CONSTRAINT IF EXISTS user_agents_user_id_fkey,
  DROP CONSTRAINT IF EXISTS user_agents_agent_id_fkey;
ALTER TABLE user_agents
  ADD CONSTRAINT user_agents_user_id_fkey  FOREIGN KEY (user_id)  REFERENCES users(id)  ON DELETE CASCADE,
  ADD CONSTRAINT user_agents_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE;

-- conversations: cascade on user delete; orphan agent_id and parent_id on agent/parent removal
ALTER TABLE conversations
  DROP CONSTRAINT IF EXISTS conversations_user_id_fkey,
  DROP CONSTRAINT IF EXISTS conversations_agent_id_fkey,
  DROP CONSTRAINT IF EXISTS conversations_parent_id_fkey;
ALTER TABLE conversations
  ADD CONSTRAINT conversations_user_id_fkey   FOREIGN KEY (user_id)   REFERENCES users(id)         ON DELETE CASCADE,
  ADD CONSTRAINT conversations_agent_id_fkey  FOREIGN KEY (agent_id)  REFERENCES agents(id)        ON DELETE SET NULL,
  ADD CONSTRAINT conversations_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES conversations(id) ON DELETE SET NULL;

-- agent_sessions: cascade on user; orphan agent_id
ALTER TABLE agent_sessions
  DROP CONSTRAINT IF EXISTS agent_sessions_user_id_fkey,
  DROP CONSTRAINT IF EXISTS agent_sessions_agent_id_fkey;
ALTER TABLE agent_sessions
  ADD CONSTRAINT agent_sessions_user_id_fkey  FOREIGN KEY (user_id)  REFERENCES users(id)  ON DELETE CASCADE,
  ADD CONSTRAINT agent_sessions_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE SET NULL;

-- agent_messages: cascade on conversation delete; orphan session_id
ALTER TABLE agent_messages
  DROP CONSTRAINT IF EXISTS agent_messages_conversation_id_fkey,
  DROP CONSTRAINT IF EXISTS agent_messages_session_id_fkey;
ALTER TABLE agent_messages
  ADD CONSTRAINT agent_messages_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES conversations(id)  ON DELETE CASCADE,
  ADD CONSTRAINT agent_messages_session_id_fkey      FOREIGN KEY (session_id)      REFERENCES agent_sessions(id) ON DELETE SET NULL;

-- agent_memory: cascade on user delete
ALTER TABLE agent_memory
  DROP CONSTRAINT IF EXISTS agent_memory_user_id_fkey;
ALTER TABLE agent_memory
  ADD CONSTRAINT agent_memory_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- query_registry: cascade on user delete
ALTER TABLE query_registry
  DROP CONSTRAINT IF EXISTS query_registry_user_id_fkey;
ALTER TABLE query_registry
  ADD CONSTRAINT query_registry_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

-- tool_calls: orphan session_id
ALTER TABLE tool_calls
  DROP CONSTRAINT IF EXISTS tool_calls_session_id_fkey;
ALTER TABLE tool_calls
  ADD CONSTRAINT tool_calls_session_id_fkey FOREIGN KEY (session_id) REFERENCES agent_sessions(id) ON DELETE SET NULL;

-- llm_usage: orphan session_id
ALTER TABLE llm_usage
  DROP CONSTRAINT IF EXISTS llm_usage_session_id_fkey;
ALTER TABLE llm_usage
  ADD CONSTRAINT llm_usage_session_id_fkey FOREIGN KEY (session_id) REFERENCES agent_sessions(id) ON DELETE SET NULL;
