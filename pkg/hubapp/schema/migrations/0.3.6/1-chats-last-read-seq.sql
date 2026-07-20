-- Stage 2 (console notifications): per-chat last-read cursor. The frontend
-- writes it via POST /api/v1/chats/{id}/read after viewing a chat; unread
-- badges + the bell derive from (session last_seq − last_read_seq). Default 0
-- means "nothing read yet". Idempotent.
ALTER TABLE chats ADD COLUMN IF NOT EXISTS last_read_seq INTEGER NOT NULL DEFAULT 0;
