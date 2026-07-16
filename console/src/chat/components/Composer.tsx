import { useLayoutEffect, useRef, useState } from 'react'
import { Send } from 'lucide-react'
import { cn } from '@/lib/cn'

export function Composer({
  running,
  runningLabel,
  onSend,
  onCancel,
}: {
  running: boolean
  runningLabel: string
  onSend: (text: string) => void
  onCancel: () => void
}) {
  const [draft, setDraft] = useState('')
  const ref = useRef<HTMLTextAreaElement>(null)

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(120, Math.max(22, el.scrollHeight)) + 'px'
  }, [draft])

  const send = () => {
    const text = draft.trim()
    if (!text) return
    onSend(text)
    setDraft('')
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  const canSend = draft.trim().length > 0

  return (
    <div className="flex flex-col gap-1.5 bg-bg px-[18px] pb-3.5 pt-2.5">
      {running && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-text2 animate-pulse">{runningLabel}</span>
          <span className="flex-1" />
          <button
            onClick={onCancel}
            className="rounded-btn border border-border bg-surface px-2.5 py-0.5 text-xs font-semibold text-red hover:bg-red-soft"
          >
            ■ Cancel turn
          </button>
        </div>
      )}
      <div className="flex items-end gap-2 rounded-composer border border-border2 bg-surface py-2 pl-3.5 pr-2 shadow-card">
        <textarea
          ref={ref}
          rows={1}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder="Message the agent… (Enter to send)"
          className="max-h-[120px] min-h-[22px] flex-1 resize-none border-none bg-transparent py-1 text-[13px] text-text placeholder:text-text3 focus:outline-none"
        />
        <button
          onClick={send}
          title="Send"
          disabled={!canSend}
          className={cn(
            'flex h-8 w-8 flex-none items-center justify-center rounded-btn text-accent-text transition-colors',
            canSend ? 'bg-accent hover:bg-accent-hi' : 'bg-surface3 text-text3',
          )}
        >
          <Send className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  )
}
