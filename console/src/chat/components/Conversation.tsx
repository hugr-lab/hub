import { useEffect, useRef } from 'react'
import { cn } from '@/lib/cn'
import type { ChatView } from '../frames'
import { statusMeta } from '../frames'
import { MessageItem } from './MessageItem'
import { Composer } from './Composer'

export function Conversation({
  view,
  running,
  chatName,
  agentName,
  narrow,
  onToggleNarrow,
  liveOpen,
  onToggleLive,
  artifactsOpen,
  onToggleArtifacts,
  artifactCount,
  onSend,
  onCancel,
  onOpenArtifacts,
}: {
  view: ChatView
  running: boolean
  chatName: string
  agentName?: string
  narrow: boolean
  onToggleNarrow: () => void
  liveOpen: boolean
  onToggleLive: () => void
  artifactsOpen: boolean
  onToggleArtifacts: () => void
  artifactCount: number
  onSend: (text: string) => void
  onCancel: () => void
  onOpenArtifacts: () => void
}) {
  const listRef = useRef<HTMLDivElement>(null)
  const meta = statusMeta(view.status)

  // Autoscroll to bottom on new content.
  useEffect(() => {
    const el = listRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [view.items])

  const runningLabel =
    view.status === 'wait_approval'
      ? 'Agent is waiting for your approval'
      : view.statusReason
        ? `Agent is working — ${view.statusReason}`
        : 'Agent is working…'

  const toggleBtn =
    'h-7 rounded-[7px] border border-border bg-surface px-2.5 text-xs font-medium hover:bg-surface2'

  return (
    <div className="flex min-w-0 flex-1 flex-col items-center bg-bg">
      <div
        className={cn(
          'flex min-h-0 w-full flex-1 flex-col transition-[max-width]',
          narrow && 'border-x border-border',
        )}
        style={{ maxWidth: narrow ? 360 : 860 }}
      >
        {/* header */}
        <div className="flex items-center gap-2.5 border-b border-border bg-surface px-[18px] py-2.5">
          <div className="flex min-w-0 flex-col">
            <span className="truncate text-[13px] font-semibold">{chatName}</span>
            {agentName && <span className="text-xs text-text3">{agentName}</span>}
          </div>
          <span className="flex-1" />
          <div
            className="flex items-center gap-1.5 rounded-full bg-surface2 px-2.5 py-[3px] text-xs font-medium"
            style={{ color: meta.color }}
          >
            <span
              className={cn('h-[7px] w-[7px] rounded-full', meta.pulse && 'animate-pulse')}
              style={{ background: meta.color }}
            />
            <span>{meta.label}</span>
          </div>
          <button
            onClick={onToggleNarrow}
            title="Preview at embedded panel width (JupyterLab)"
            className={cn(toggleBtn, narrow && 'text-accent')}
          >
            {narrow ? '⇤ Full width' : '⇥ Panel 360px'}
          </button>
          <button onClick={onToggleLive} className={cn(toggleBtn, liveOpen && 'text-accent')}>
            Live view
          </button>
          <button onClick={onToggleArtifacts} className={cn(toggleBtn, artifactsOpen && 'text-accent')}>
            Artifacts · {artifactCount}
          </button>
        </div>

        {/* messages */}
        <div
          ref={listRef}
          aria-live="polite"
          className="flex min-h-0 flex-1 flex-col gap-2.5 overflow-y-auto px-[18px] pb-2 pt-[18px]"
        >
          {view.items.length === 0 && (
            <div className="m-auto max-w-xs text-center text-xs text-text3">
              Send a message to start the conversation with {agentName ?? 'the agent'}.
            </div>
          )}
          {view.items.map((item) => (
            <MessageItem key={item.id} item={item} onOpenArtifacts={onOpenArtifacts} />
          ))}
          <div className="h-1.5" />
        </div>

        <Composer running={running} runningLabel={runningLabel} onSend={onSend} onCancel={onCancel} />
      </div>
    </div>
  )
}
