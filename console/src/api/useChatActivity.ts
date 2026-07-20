import { useQuery } from '@tanstack/react-query'
import { fetchChatActivity, type ChatActivity } from './notifications'

/** Shared poller for chat activity (unread + last-event). Both the bell and the
 *  chat-list badges call this — one query key → one poll, deduped by React
 *  Query. ~20s cadence keeps the hub load at O(users). */
export function useChatActivity() {
  return useQuery<ChatActivity[]>({
    queryKey: ['chat-activity'],
    queryFn: fetchChatActivity,
    refetchInterval: 20_000,
    staleTime: 15_000,
    refetchOnWindowFocus: true,
  })
}
