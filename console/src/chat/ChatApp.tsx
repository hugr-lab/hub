import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
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

  const chat = useChat(client, chatId ?? null)

  // Panel state — live XOR artifacts; hidden in narrow mode.
  const [narrow, setNarrow] = useState(false)
  const [panel, setPanel] = useState<'none' | 'live' | 'artifacts'>('none')
  const showPanel = !narrow && panel !== 'none'

  const createChat = (aid: string) => {
    client.createChat(aid).then((c) => {
      chatsQ.refetch()
      select(c.id)
    })
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
        />
      )}

      {chatId ? (
        <Conversation
          view={chat.view}
          running={chat.running}
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
        />
      ) : (
        <div className="flex flex-1 items-center justify-center bg-bg text-sm text-text3">
          Pick a chat or start a new one.
        </div>
      )}

      {showPanel && panel === 'live' && <LiveViewPanel view={chat.view} />}
      {showPanel && panel === 'artifacts' && (
        <ArtifactsPanel
          artifacts={chat.artifacts}
          onUpload={chat.uploadArtifact}
          onDownload={chat.downloadArtifact}
        />
      )}

      {chat.view.inquiry && <InquiryModal inquiry={chat.view.inquiry} onAnswer={chat.answerInquiry} />}
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
