import { useState } from 'react'
import { Folder } from 'lucide-react'
import { cn } from '@/lib/cn'
import type { Chat, PickableAgent } from '../client'

export interface ChatGroup {
  name: string
  chats: Chat[]
}

export function ChatRail({
  groups,
  activeChatId,
  pickableAgents,
  onSelectChat,
  onCreateChat,
}: {
  groups: ChatGroup[]
  activeChatId: string | null
  pickableAgents: PickableAgent[]
  onSelectChat: (id: string) => void
  onCreateChat: (agentId: string) => void
}) {
  const [pickerOpen, setPickerOpen] = useState(false)

  return (
    <div className="flex w-[248px] flex-none flex-col border-r border-border bg-surface">
      <div className="flex flex-col gap-2 p-3 pb-2">
        <button
          onClick={() => setPickerOpen((o) => !o)}
          className="flex w-full items-center justify-center gap-1.5 rounded-btn bg-accent py-1.5 text-xs font-semibold text-accent-text hover:bg-accent-hi"
        >
          ＋ New chat
        </button>
        {pickerOpen && (
          <div className="flex flex-col gap-1 rounded-panel border border-border bg-surface2 p-2 animate-fadeUp">
            <div className="eyebrow px-1 pb-0.5">Pick an agent</div>
            {pickableAgents.map((pa) => (
              <button
                key={pa.id}
                onClick={() => {
                  onCreateChat(pa.id)
                  setPickerOpen(false)
                }}
                className="flex items-center gap-2 rounded-md px-1.5 py-1.5 text-left hover:bg-surface3"
              >
                <span className="h-[7px] w-[7px] flex-none rounded-full" style={{ background: dotFor(pa.status) }} />
                <span className="flex-1 text-xs font-medium">{pa.name}</span>
                <span className="text-2xs text-text3">{pa.access}</span>
              </button>
            ))}
          </div>
        )}
      </div>

      <div className="flex flex-1 flex-col gap-3 overflow-y-auto px-2 pb-3">
        {groups.map((g) => (
          <div key={g.name} className="flex flex-col gap-px">
            <div className="flex items-center gap-1.5 px-2 pb-1 pt-0.5">
              <Folder className="h-3 w-3 text-text3" />
              <span className="eyebrow">{g.name}</span>
            </div>
            {g.chats.map((ch) => {
              const active = ch.id === activeChatId
              return (
                <button
                  key={ch.id}
                  onClick={() => onSelectChat(ch.id)}
                  className={cn(
                    'flex flex-col gap-px rounded-btn px-2.5 py-1.5 text-left',
                    active ? 'bg-accent-soft' : 'hover:bg-surface2',
                  )}
                >
                  <span
                    className={cn(
                      'w-full truncate text-xs',
                      active ? 'font-semibold text-accent' : 'font-medium text-text',
                    )}
                  >
                    {ch.name}
                  </span>
                  <span className="truncate text-2xs text-text3">
                    {ch.agent_name ?? ch.agent_id}
                    {ch.last ? ` · ${ch.last}` : ''}
                  </span>
                </button>
              )
            })}
          </div>
        ))}
      </div>
    </div>
  )
}

function dotFor(status?: string): string {
  if (status === 'running') return 'var(--green)'
  if (status === 'starting' || status === 'loading') return 'var(--amber)'
  if (status === 'error') return 'var(--red)'
  return 'var(--text3)'
}
