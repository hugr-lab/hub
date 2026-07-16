import { useState } from 'react'
import { ChevronRight, FileText, Wrench } from 'lucide-react'
import { cn } from '@/lib/cn'
import type { RenderItem } from '../frames'

export function MessageItem({
  item,
  onOpenArtifacts,
}: {
  item: RenderItem
  onOpenArtifacts?: () => void
}) {
  switch (item.kind) {
    case 'user':
      return (
        <div className="flex flex-col">
          <div className="max-w-[78%] self-end whitespace-pre-wrap rounded-[13px_13px_3px_13px] bg-accent px-[13px] py-2 text-[13px] text-accent-text animate-fadeUp">
            {item.text}
          </div>
        </div>
      )

    case 'agent':
      return (
        <div className="flex flex-col">
          <div className="flex max-w-[86%] flex-col gap-1.5 self-start animate-fadeUp">
            <div className="whitespace-pre-wrap rounded-[13px_13px_13px_3px] border border-border bg-surface px-3.5 py-2.5 text-[13px]">
              {item.text}
              {item.streaming && (
                <span className="ml-0.5 inline-block h-[13px] w-[7px] translate-y-0.5 rounded-[1px] bg-accent align-text-bottom animate-blinkc" />
              )}
            </div>
            {item.usage && (
              <div className="pl-1 font-mono text-2xs text-text3">final · {item.usage}</div>
            )}
          </div>
        </div>
      )

    case 'reasoning':
      return <ReasoningItem item={item} />

    case 'tool':
      return <ToolItem item={item} />

    case 'artifact':
      return (
        <button
          onClick={onOpenArtifacts}
          className="flex items-center gap-2.5 self-start rounded-panel border border-border bg-surface px-3 py-1.5 hover:border-accent animate-fadeUp"
        >
          <span className="flex h-[26px] w-[26px] items-center justify-center rounded-md bg-accent-soft text-accent">
            <FileText className="h-3.5 w-3.5" />
          </span>
          <span className="flex flex-col items-start">
            <span className="font-mono text-xs font-semibold">{item.name}</span>
            <span className="text-2xs text-text3">
              artifact_produced{item.size ? ` · ${item.size}` : ''}
            </span>
          </span>
        </button>
      )

    case 'system':
      return (
        <div
          className={cn(
            'self-center rounded-full bg-surface2 px-3 py-[3px] text-xs animate-fadeUp',
            item.tone === 'error' ? 'text-red' : 'text-text3',
          )}
        >
          {item.text}
        </div>
      )
  }
}

function ReasoningItem({ item }: { item: Extract<RenderItem, { kind: 'reasoning' }> }) {
  const [open, setOpen] = useState(false)
  const label = item.streaming
    ? 'Thinking…'
    : item.elapsedMs
      ? `Thought for ${(item.elapsedMs / 1000).toFixed(1)}s`
      : 'Thought'
  return (
    <div className="w-full max-w-[86%] self-start animate-fadeUp">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1.5 px-1 py-0.5 text-xs font-medium text-text3 hover:text-text2"
      >
        <ChevronRight className={cn('h-3.5 w-3.5 transition-transform', open && 'rotate-90')} />
        <span className={cn(item.streaming && 'animate-pulse')}>{label}</span>
      </button>
      {open && item.text && (
        <div className="ml-3.5 mt-1 whitespace-pre-wrap border-l-2 border-border2 px-3 py-1 text-xs italic text-text2">
          {item.text}
        </div>
      )}
    </div>
  )
}

function ToolItem({ item }: { item: Extract<RenderItem, { kind: 'tool' }> }) {
  const [open, setOpen] = useState(false)
  const stateLabel = item.state === 'running' ? 'running…' : item.state === 'error' ? 'error' : 'done'
  const stateColor =
    item.state === 'running' ? 'text-amber' : item.state === 'error' ? 'text-red' : 'text-green'
  return (
    <div className="w-full max-w-[86%] self-start animate-fadeUp">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-2 rounded-btn border border-border bg-surface2 px-2.5 py-1.5 text-xs text-text2 hover:bg-surface3"
      >
        <ChevronRight className={cn('h-3 w-3 text-text3 transition-transform', open && 'rotate-90')} />
        <Wrench className="h-3 w-3" />
        <span className="font-mono font-semibold">{item.name}</span>
        <span className={cn('font-medium', stateColor, item.state === 'running' && 'animate-pulse')}>
          {stateLabel}
        </span>
      </button>
      {open && (
        <div className="ml-3.5 mt-1 flex flex-col gap-1">
          <div className="whitespace-pre-wrap break-all rounded-btn border border-border bg-surface2 px-2.5 py-1.5 font-mono text-[11px] text-text2">
            {item.args}
          </div>
          {item.result != null && (
            <div className="whitespace-pre-wrap break-all rounded-btn border border-border border-l-[3px] border-l-green bg-surface px-2.5 py-1.5 font-mono text-[11px] text-text2">
              → {item.result}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
