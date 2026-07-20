import { restJSON, restRaw } from '@/lib/rest'

/** One chat's unread summary from GET /api/v1/chats/activity (Stage 2). */
export interface ChatActivity {
  chat_id: string
  title: string
  agent_id: string
  last_seq: number
  unread: number
  last_event?: { kind: string; at: string }
}

/** Poll the caller's chat activity — unread counts + last-event kind per chat.
 *  Drives the bell + chat-list badges. Cheap hub aggregate (no per-chat SSE). */
export async function fetchChatActivity(): Promise<ChatActivity[]> {
  const body = await restJSON<{ chats?: ChatActivity[] }>('/api/v1/chats/activity')
  return body?.chats ?? []
}

/** Advance a chat's read cursor to seq (CAS server-side — only moves forward). */
export async function markChatRead(chatId: string, seq: number): Promise<void> {
  if (seq <= 0) return
  await restRaw(`/api/v1/chats/${chatId}/read`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ seq }),
  })
}

/** Human label + tone for a notable event kind (bell line rendering). */
export function eventKindLabel(kind?: string): { text: string; dot: string } {
  switch (kind) {
    case 'inquiry_request':
      return { text: 'is waiting for your approval', dot: 'var(--amber)' }
    case 'agent_message':
      return { text: 'replied', dot: 'var(--green)' }
    default:
      return { text: 'new activity', dot: 'var(--text3)' }
  }
}
