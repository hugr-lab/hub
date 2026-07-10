-- HB5 G3 live-gate finding: pre-HB4 databases carry an FK
-- user_agents.agent_id → hub.db.agents, but agent identity moved to the
-- Agent DB (hub.agent.db.agents) with the HB4 spawn rewire — the platform
-- copy is legacy and no longer populated, so every grant to a real agent
-- fails the constraint. The reference is LOGICAL (cross-source) by design;
-- fresh init.sql never had the FK.
ALTER TABLE user_agents DROP CONSTRAINT IF EXISTS user_agents_agent_id_fkey;
