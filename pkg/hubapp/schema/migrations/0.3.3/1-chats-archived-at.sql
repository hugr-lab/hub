-- HB5 G3: archive flag on chat threads (spec-hub-gateway §3).
-- NULL = live chat; a timestamp = when the user archived it.
ALTER TABLE chats ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;
