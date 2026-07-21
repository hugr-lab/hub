import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { cn } from '@/lib/cn'
import { ChatClient, type Chat } from './client'
import { useChat } from './useChat'
import { ChatRail, type ChatGroup } from './components/ChatRail'
import { Conversation } from './components/Conversation'
import { LiveViewPanel } from './components/LiveViewPanel'
import { ArtifactsPanel } from './components/ArtifactsPanel'
import { InquiryModal } from './components/InquiryModal'

export interface ChatAppProps {
  apiBase: string
  getToken: () => Promise<string | null> | string | null
  /** Controlled active chat (e.g. bound to the SPA URL). */
  chatId?: string | null
  /** Start a new chat with this agent if no chat is selected. */
  agentId?: string
  theme?: 'light' | 'dark'
  /** Show the projects/chats rail (default true; a Jupyter panel may hide it). */
  showRail?: boolean
  demo?: boolean
  onChatChange?: (chatId: string) => void
}

export function ChatApp(props: ChatAppProps) {
  const { apiBase, getToken, theme, showRail = true, demo, agentId, onChatChange } = props

  // Stable client; always reads the freshest token.
  const getTokenRef = useRef(getToken)
  getTokenRef.current = getToken
  const client = useMemo(
    () => new ChatClient({ apiBase, demo, getToken: () => getTokenRef.current() }),
    [apiBase, demo],
  )

  const [internalChatId, setInternalChatId] = useState<string | null>(props.chatId ?? null)
  const chatId = props.chatId !== undefined ? props.chatId : internalChatId

  const select = (id: string) => {
    setInternalChatId(id)
    onChatChange?.(id)
  }

  const projectsQ = useQuery({ queryKey: ['chat', 'projects'], queryFn: () => client.listProjects() })
  const chatsQ = useQuery({ queryKey: ['chats'], queryFn: () => client.listChats() })
  const agentsQ = useQuery({ queryKey: ['chat', 'pickable'], queryFn: () => client.listPickableAgents() })

  const chats = chatsQ.data ?? []
  const projects = projectsQ.data ?? []

  // Auto-select / auto-create once data is available.
  const bootstrapped = useRef(false)
  useEffect(() => {
    if (bootstrapped.current || chatId) return
    if (agentId) {
      bootstrapped.current = true
      client.createChat(agentId).then((c) => {
        chatsQ.refetch()
        select(c.id)
      })
    } else if (showRail && chats.length > 0) {
      bootstrapped.current = true
      select(chats[0].id)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chats, agentId, chatId])

  const groups = useMemo<ChatGroup[]>(() => groupChats(chats, projects), [chats, projects])
  const activeChat = chats.find((c) => c.id === chatId) ?? null

  // Is the active chat's agent container up? Drives the "stopped" vs "warming up"
  // hint. Unknown (agent absent from the pickable list / list still loading) is
  // treated as running so a fresh chat's not-yet-connected stream never flashes
  // the scary "it looks stopped" message for an agent that is actually running.
  const activeAgent = (agentsQ.data ?? []).find((a) => a.id === activeChat?.agent_id)
  const agentRunning = activeAgent ? activeAgent.status === 'running' || activeAgent.status === 'starting' : true

  const chat = useChat(client, chatId ?? null)

  // Panel state — live XOR artifacts; hidden in narrow mode.
  const [narrow, setNarrow] = useState(false)
  const [panel, setPanel] = useState<'none' | 'live' | 'artifacts'>('none')
  const [answerError, setAnswerError] = useState<string | null>(null)
  const showPanel = !narrow && panel !== 'none'

  const createChat = (aid: string) => {
    client.createChat(aid).then((c) => {
      chatsQ.refetch()
      select(c.id)
    })
  }

  // Chats that already carry a non-default / pinned title (recap won't rename).
  const titledRef = useRef<Set<string>>(new Set())
  const isDefaultTitle = (name?: string) => !name || /^new chat$/i.test(name.trim())

  // Auto-name a still-default chat from the recap topic, once. A manual rename
  // pins the name (adds it to titledRef), so the recap never overrides it.
  useEffect(() => {
    const topic = chat.view.recap?.topic?.trim()
    if (!chatId || !topic || !activeChat) return
    if (titledRef.current.has(chatId) || !isDefaultTitle(activeChat.name)) return
    titledRef.current.add(chatId)
    client.updateChat(chatId, { name: topic }).then(() => chatsQ.refetch()).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chat.view.recap?.topic, chatId, activeChat])

  const renameChat = (name: string) => {
    if (!chatId) return
    titledRef.current.add(chatId)
    client.updateChat(chatId, { name }).then(() => chatsQ.refetch()).catch(() => {})
  }

  // Rename any chat from the rail (without opening it). Pins the title so the
  // recap auto-namer won't override it.
  const renameChatById = (id: string, name: string) => {
    titledRef.current.add(id)
    client
      .updateChat(id, { name })
      .then(() => {
        chatsQ.refetch()
        if (railMode === 'closed') loadClosed(true)
      })
      .catch(() => {})
  }

  // ── chat lifecycle: close (archive) / restore / delete ──
  const [railMode, setRailMode] = useState<'live' | 'closed'>('live')
  const [closed, setClosed] = useState<Chat[]>([])
  const [closedDone, setClosedDone] = useState(false)
  const [closedLoading, setClosedLoading] = useState(false)

  // Keyset-paginated archived chats. reset=true refetches the first page.
  const loadClosed = useCallback(
    async (reset: boolean) => {
      setClosedLoading(true)
      try {
        const last = reset ? undefined : closed[closed.length - 1]
        const cursor = last ? { beforeActiveAt: last.last_active_at ?? '', beforeId: last.id } : undefined
        const page = await client.listChats(true, cursor, 50)
        // Mark dropped chats (terminated session) so they render read-only —
        // archive = resumable (Restore), drop = terminal (no Restore).
        const sids = page.map((c) => c.root_session_id).filter(Boolean) as string[]
        const statuses = await client.sessionStatuses(sids).catch(() => ({}) as Record<string, string>)
        const marked = page.map((c) => ({
          ...c,
          dropped: c.root_session_id ? statuses[c.root_session_id] === 'terminated' : false,
        }))
        setClosed((prev) => (reset ? marked : [...prev, ...marked]))
        setClosedDone(page.length < 50)
      } finally {
        setClosedLoading(false)
      }
    },
    [client, closed],
  )

  const setRailModeAndLoad = (m: 'live' | 'closed') => {
    setRailMode(m)
    if (m === 'closed') loadClosed(true)
  }

  const [pendingAction, setPendingAction] = useState<{ id: string; kind: 'archive' | 'drop' } | null>(null)
  const runAction = (id: string, kind: 'archive' | 'drop') => {
    setPendingAction(null)
    // Leave the archived chat's view — it's not the live conversation anymore.
    if (id === chatId) select('')
    const p = kind === 'drop' ? client.dropChat(id) : client.archiveChat(id)
    p.then(() => chatsQ.refetch()).catch(() => {})
  }
  const archiveChat = (id: string) => {
    // Archive cancels the current turn — warn only when the open chat is working.
    if (id === chatId && chat.running) {
      setPendingAction({ id, kind: 'archive' })
      return
    }
    runAction(id, 'archive')
  }
  const dropChat = (id: string) => {
    // Drop terminates the session irreversibly — always confirm.
    setPendingAction({ id, kind: 'drop' })
  }
  const restoreChat = (id: string) => {
    client
      .updateChat(id, { archived: false })
      .then(() => {
        chatsQ.refetch()
        loadClosed(true)
      })
      .catch(() => {})
  }

  return (
    <div
      data-theme={theme}
      className="flex min-h-0 flex-1 flex-row bg-bg text-text"
      style={{ height: '100%' }}
    >
      {showRail && (
        <ChatRail
          groups={groups}
          activeChatId={chatId ?? null}
          pickableAgents={agentsQ.data ?? []}
          onSelectChat={select}
          onCreateChat={createChat}
          mode={railMode}
          onSetMode={setRailModeAndLoad}
          closedChats={closed}
          closedDone={closedDone}
          closedLoading={closedLoading}
          onLoadMoreClosed={() => loadClosed(false)}
          onArchiveChat={archiveChat}
          onDropChat={dropChat}
          onRestoreChat={restoreChat}
          onRenameChat={renameChatById}
        />
      )}

      {chatId ? (
        <Conversation
          view={chat.view}
          running={chat.running}
          loading={!chat.connected}
          unreachable={chat.unreachable}
          agentRunning={agentRunning}
          chatId={chatId}
          chatName={activeChat?.name ?? 'Chat'}
          agentName={activeChat?.agent_name ?? activeChat?.agent_id}
          narrow={narrow}
          onToggleNarrow={() => setNarrow((n) => !n)}
          liveOpen={panel === 'live'}
          onToggleLive={() => setPanel((p) => (p === 'live' ? 'none' : 'live'))}
          artifactsOpen={panel === 'artifacts'}
          onToggleArtifacts={() => setPanel((p) => (p === 'artifacts' ? 'none' : 'artifacts'))}
          artifactCount={chat.artifacts.length}
          onSend={chat.sendMessage}
          onCancel={chat.cancelTurn}
          onOpenArtifacts={() => setPanel('artifacts')}
          onRename={renameChat}
        />
      ) : (
        <div className="flex flex-1 items-center justify-center bg-bg text-sm text-text3">
          Pick a chat or start a new one.
        </div>
      )}

      {showPanel && panel === 'live' && (
        <LiveViewPanel
          view={chat.view}
          tasks={chat.tasks}
          archivedTaskCount={chat.archivedTaskCount}
          scheduleScope={chat.scheduleScope}
          onToggleScheduleScope={chat.toggleScheduleScope}
          onCancelTask={chat.cancelTask}
          onSetTaskPaused={chat.setTaskPaused}
        />
      )}
      {showPanel && panel === 'artifacts' && (
        <ArtifactsPanel
          artifacts={chat.artifacts}
          onUpload={chat.uploadArtifact}
          onDownload={chat.downloadArtifact}
        />
      )}

      {chat.view.inquiry && (
        <InquiryModal
          inquiry={chat.view.inquiry}
          onAnswer={(a) => {
            setAnswerError(null)
            chat.answerInquiry(a).catch((e) =>
              setAnswerError(e instanceof Error ? e.message : 'Failed to submit answer'),
            )
          }}
        />
      )}
      {answerError && (
        <div className="fixed bottom-4 left-1/2 z-[200] -translate-x-1/2 rounded-btn border border-red bg-surface px-3.5 py-2 text-xs text-red shadow-lg">
          Inquiry answer failed: {answerError}
        </div>
      )}
      {pendingAction && (
        <div
          className="fixed inset-0 z-[200] flex items-center justify-center bg-black/30"
          onClick={() => setPendingAction(null)}
        >
          <div
            className="w-[360px] rounded-panel border border-border bg-surface p-4 shadow-lg animate-fadeUp"
            onClick={(e) => e.stopPropagation()}
          >
            {pendingAction.kind === 'drop' ? (
              <>
                <div className="text-sm font-semibold">Drop this chat?</div>
                <p className="mt-1.5 text-xs leading-snug text-text2">
                  This ends the session permanently (<span className="font-mono">/end</span>) and
                  archives the chat. History is kept but read-only — it <b>cannot be restored to
                  continue</b>. To just pause it, use Archive instead.
                </p>
              </>
            ) : (
              <>
                <div className="text-sm font-semibold">Archive this chat?</div>
                <p className="mt-1.5 text-xs leading-snug text-text2">
                  The agent is working. Archiving cancels the current turn (and any running
                  missions). The session stays intact — restore the chat later to continue.
                </p>
              </>
            )}
            <div className="mt-3.5 flex justify-end gap-2">
              <button
                onClick={() => setPendingAction(null)}
                className="rounded-btn border border-border px-3 py-1.5 text-xs font-semibold text-text2 hover:bg-surface2"
              >
                Cancel
              </button>
              <button
                onClick={() => runAction(pendingAction.id, pendingAction.kind)}
                className={cn(
                  'rounded-btn px-3 py-1.5 text-xs font-semibold text-white hover:opacity-90',
                  pendingAction.kind === 'drop' ? 'bg-red' : 'bg-accent',
                )}
              >
                {pendingAction.kind === 'drop' ? 'Drop' : 'Archive'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function groupChats(chats: Chat[], projects: { id: string; name: string }[]): ChatGroup[] {
  const nameById = new Map(projects.map((p) => [p.id, p.name]))
  const byProject = new Map<string, Chat[]>()
  for (const c of chats) {
    const key = c.project_id ?? '__direct__'
    const arr = byProject.get(key) ?? []
    arr.push(c)
    byProject.set(key, arr)
  }
  const groups: ChatGroup[] = []
  for (const [key, list] of byProject) {
    groups.push({ name: key === '__direct__' ? 'Direct chats' : (nameById.get(key) ?? 'Project'), chats: list })
  }
  return groups
}
