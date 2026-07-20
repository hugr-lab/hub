import { useState } from 'react'
import { Folder, MoreHorizontal } from 'lucide-react'
import { cn } from '@/lib/cn'
import { Menu, MenuContent, MenuItem, MenuTrigger } from '@/components/ui'
import { useChatActivity } from '@/api/useChatActivity'
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
  mode = 'live',
  onSetMode,
  closedChats = [],
  closedDone = true,
  closedLoading = false,
  onLoadMoreClosed,
  onArchiveChat,
  onDropChat,
  onRestoreChat,
  onRenameChat,
}: {
  groups: ChatGroup[]
  activeChatId: string | null
  pickableAgents: PickableAgent[]
  onSelectChat: (id: string) => void
  onCreateChat: (agentId: string) => void
  mode?: 'live' | 'closed'
  onSetMode?: (m: 'live' | 'closed') => void
  closedChats?: Chat[]
  closedDone?: boolean
  closedLoading?: boolean
  onLoadMoreClosed?: () => void
  onArchiveChat?: (id: string) => void
  onDropChat?: (id: string) => void
  onRestoreChat?: (id: string) => void
  onRenameChat?: (id: string, name: string) => void
}) {
  const [pickerOpen, setPickerOpen] = useState(false)
  const { data: activity = [] } = useChatActivity()
  const unreadBy = new Map(activity.map((a) => [a.chat_id, a.unread]))
  const closedView = mode === 'closed'

  return (
    <div className="flex w-[248px] flex-none flex-col border-r border-border bg-surface">
      <div className="flex flex-col gap-2 p-3 pb-2">
        {closedView ? (
          <button
            onClick={() => onSetMode?.('live')}
            className="flex w-full items-center justify-center gap-1.5 rounded-btn border border-border bg-surface py-1.5 text-xs font-semibold text-text2 hover:bg-surface2"
          >
            ← Back to chats
          </button>
        ) : (
          <>
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
          </>
        )}
      </div>

      <div className="flex flex-1 flex-col gap-3 overflow-y-auto px-2 pb-3">
        {closedView ? (
          <div className="flex flex-col gap-px">
            <div className="flex items-center gap-1.5 px-2 pb-1 pt-0.5">
              <span className="eyebrow">Closed chats</span>
            </div>
            {closedChats.length === 0 && !closedLoading && (
              <div className="px-2.5 py-4 text-center text-2xs text-text3">No closed chats.</div>
            )}
            {closedChats.map((ch) => (
              <ChatRow
                key={ch.id}
                ch={ch}
                active={ch.id === activeChatId}
                unread={0}
                closed
                onSelect={onSelectChat}
                onRestore={onRestoreChat}
                onRename={onRenameChat}
              />
            ))}
            {!closedDone && (
              <button
                onClick={onLoadMoreClosed}
                disabled={closedLoading}
                className="mx-2 mt-1 rounded-btn border border-border py-1.5 text-2xs font-semibold text-text2 hover:bg-surface2 disabled:opacity-50"
              >
                {closedLoading ? 'Loading…' : 'Load more'}
              </button>
            )}
          </div>
        ) : (
          groups.map((g) => (
            <div key={g.name} className="flex flex-col gap-px">
              <div className="flex items-center gap-1.5 px-2 pb-1 pt-0.5">
                <Folder className="h-3 w-3 text-text3" />
                <span className="eyebrow">{g.name}</span>
              </div>
              {g.chats.map((ch) => (
                <ChatRow
                  key={ch.id}
                  ch={ch}
                  active={ch.id === activeChatId}
                  unread={ch.id === activeChatId ? 0 : unreadBy.get(ch.id) ?? 0}
                  onSelect={onSelectChat}
                  onArchive={onArchiveChat}
                  onDrop={onDropChat}
                  onRename={onRenameChat}
                />
              ))}
            </div>
          ))
        )}
      </div>

      {!closedView && onSetMode && (
        <button
          onClick={() => onSetMode('closed')}
          className="flex-none border-t border-border px-3 py-2 text-left text-2xs font-semibold text-text3 hover:bg-surface2"
        >
          Closed chats →
        </button>
      )}
    </div>
  )
}

// ChatRow is one thread with a hover-revealed "…" menu (Close/Restore/Delete).
// A div (not a button) so the menu trigger isn't nested inside a button.
function ChatRow({
  ch,
  active,
  unread,
  closed = false,
  onSelect,
  onArchive,
  onDrop,
  onRestore,
  onRename,
}: {
  ch: Chat
  active: boolean
  unread: number
  closed?: boolean
  onSelect: (id: string) => void
  onArchive?: (id: string) => void
  onDrop?: (id: string) => void
  onRestore?: (id: string) => void
  onRename?: (id: string, name: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')
  const commit = () => {
    setEditing(false)
    const n = draft.trim()
    if (n && n !== ch.name) onRename?.(ch.id, n)
  }

  return (
    <div
      className={cn(
        'group flex items-center rounded-btn pr-1',
        active ? 'bg-accent-soft' : 'hover:bg-surface2',
      )}
    >
      {editing ? (
        <input
          autoFocus
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => {
            if (e.key === 'Enter') commit()
            else if (e.key === 'Escape') setEditing(false)
          }}
          className="min-w-0 flex-1 border-b border-accent bg-transparent px-2.5 py-1.5 text-xs font-medium text-text focus:outline-none"
        />
      ) : (
        <button
          onClick={() => onSelect(ch.id)}
          className="flex min-w-0 flex-1 flex-col gap-px px-2.5 py-1.5 text-left"
        >
          <span className="flex w-full items-center gap-1.5">
            <span
              className={cn(
                'min-w-0 flex-1 truncate text-xs',
                active ? 'font-semibold text-accent' : unread > 0 ? 'font-semibold text-text' : 'font-medium text-text',
                closed && 'text-text2',
                ch.dropped && 'text-text3',
              )}
            >
              {ch.name}
            </span>
            {ch.dropped && (
              <span className="flex-none rounded-full bg-surface3 px-1.5 text-[9px] font-semibold uppercase tracking-wide text-text3">
                ended
              </span>
            )}
            {unread > 0 && (
              <span className="flex h-[15px] min-w-[15px] flex-none items-center justify-center rounded-full bg-red px-[3px] text-[9.5px] font-bold text-white">
                {unread > 99 ? '99+' : unread}
              </span>
            )}
          </span>
          <span className="truncate text-2xs text-text3">
            {ch.agent_name ?? ch.agent_id}
            {ch.last ? ` · ${ch.last}` : ''}
          </span>
        </button>
      )}
      {!editing && (closed ? !ch.dropped && !!onRestore : !!(onRename || onArchive || onDrop)) && (
        <Menu>
          <MenuTrigger asChild>
            <button
              title="Chat actions"
              onClick={(e) => e.stopPropagation()}
              className="flex h-6 w-6 flex-none items-center justify-center rounded text-text3 opacity-0 transition-opacity hover:bg-surface3 group-hover:opacity-100 data-[state=open]:opacity-100"
            >
              <MoreHorizontal className="h-[15px] w-[15px]" />
            </button>
          </MenuTrigger>
          <MenuContent align="end">
            {!closed && onRename && (
              <MenuItem
                onClick={() => {
                  setDraft(ch.name)
                  setEditing(true)
                }}
              >
                Rename
              </MenuItem>
            )}
            {closed ? (
              // Dropped chats are terminal — read-only history, no Restore.
              !ch.dropped && onRestore && <MenuItem onClick={() => onRestore(ch.id)}>Restore</MenuItem>
            ) : (
              <>
                {onArchive && <MenuItem onClick={() => onArchive(ch.id)}>Archive</MenuItem>}
                {onDrop && <MenuItem onClick={() => onDrop(ch.id)}>Drop (end session)</MenuItem>}
              </>
            )}
          </MenuContent>
        </Menu>
      )}
    </div>
  )
}

function dotFor(status?: string): string {
  if (status === 'running') return 'var(--green)'
  if (status === 'starting' || status === 'loading') return 'var(--amber)'
  if (status === 'error') return 'var(--red)'
  return 'var(--text3)'
}
