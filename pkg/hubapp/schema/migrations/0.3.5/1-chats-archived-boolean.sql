-- HB5 review finding: hugr coerces a null Timestamp mutation value to the
-- zero time instead of SQL NULL (query-engine ask #9), so an archived_at
-- timestamp could be set but never cleared — unarchive was inexpressible.
-- Pivot to a plain boolean; archive time, if ever needed for display, can be
-- derived from updated_at.
ALTER TABLE chats DROP COLUMN IF EXISTS archived_at;
ALTER TABLE chats ADD COLUMN IF NOT EXISTS archived BOOLEAN NOT NULL DEFAULT FALSE;
