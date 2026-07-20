import { useQueryClient } from '@tanstack/react-query'

interface NavCounts {
  chats?: number
  agents?: number
}

/**
 * Sidebar badge counts, read opportunistically from the query cache so they
 * light up once the Chat / Agents screens have loaded their lists. Returns
 * undefined counts until then (no badge shown).
 */
export function useNavCounts(): NavCounts {
  const qc = useQueryClient()
  const chats = qc.getQueryData<unknown[]>(['chats'])
  const agents = qc.getQueryData<unknown[]>(['agents'])
  return {
    chats: Array.isArray(chats) ? chats.length : undefined,
    agents: Array.isArray(agents) ? agents.length : undefined,
  }
}
