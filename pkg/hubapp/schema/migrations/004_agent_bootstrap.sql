-- 0.3.1: one-shot agent bootstrap secrets (spec-hub-side §1.5) — the
-- spawn-time credential a container redeems at /agent/token for its first
-- agent JWT. Only the sha256 hex is stored.
CREATE TABLE IF NOT EXISTS agent_bootstrap_secrets (
  id TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
  agent_id TEXT NOT NULL,
  secret_hash TEXT NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_bootstrap_secret_hash ON agent_bootstrap_secrets(secret_hash);
