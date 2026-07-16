import { cn } from '@/lib/cn'
import type { ChatView } from '../frames'
import { statusMeta } from '../frames'

function fmtTokens(n: number): string {
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n)
}

export function LiveViewPanel({ view }: { view: ChatView }) {
  const meta = statusMeta(view.status)
  const budget = view.budget
  const pct = budget && budget.limit ? Math.min(100, (budget.used / budget.limit) * 100) : 0
  const ctxColor = pct > 85 ? 'var(--red)' : pct > 60 ? 'var(--amber)' : 'var(--accent)'

  const sched = [
    { name: 'daily-revenue-report', when: 'Every day · 07:00' },
    { name: 'gateway-health-sweep', when: 'Every 15 min' },
  ]

  return (
    <div className="flex w-[280px] flex-none flex-col border-l border-border bg-surface animate-fadeUp">
      <div className="flex items-center border-b border-border px-3.5 py-2.5">
        <span className="flex-1 text-sm font-semibold">Live view</span>
        <span className="flex items-center gap-1.5 text-xs" style={{ color: meta.color }}>
          <span
            className={cn('h-1.5 w-1.5 rounded-full', meta.pulse && 'animate-pulse')}
            style={{ background: meta.color }}
          />
          {meta.label}
        </span>
      </div>
      <div className="flex flex-1 flex-col gap-4 overflow-y-auto px-3.5 py-3">
        {/* context budget */}
        <div className="flex flex-col gap-1.5">
          <div className="flex items-baseline text-xs text-text2">
            <span className="eyebrow flex-1">Context budget</span>
            <span className="font-mono">
              {budget ? `${fmtTokens(budget.used)} / ${fmtTokens(budget.limit)}` : '—'}
            </span>
          </div>
          <div className="h-[5px] overflow-hidden rounded-full bg-surface3">
            <div className="h-full rounded-full transition-[width]" style={{ width: `${pct}%`, background: ctxColor }} />
          </div>
        </div>

        {/* missions & subagents */}
        <div className="flex flex-col gap-1">
          <span className="eyebrow">Missions &amp; subagents</span>
          {view.missions.length === 0 ? (
            <span className="py-1 text-xs text-text3">No active subagents</span>
          ) : (
            view.missions.map((m) => (
              <div
                key={m.id}
                className="flex items-center gap-2 py-1 text-xs"
                style={{ paddingLeft: m.depth * 14 }}
              >
                <span className="h-[7px] w-[7px] flex-none rounded-full" style={{ background: dotFor(m.state) }} />
                <span className="min-w-0 flex-1 truncate text-text">
                  {m.name}
                  {m.async && <span className="ml-1 text-blue">(async)</span>}
                </span>
                <span className="text-2xs font-semibold" style={{ color: dotFor(m.state) }}>
                  {m.state}
                </span>
              </div>
            ))
          )}
        </div>

        {/* scheduled tasks */}
        <div className="flex flex-col gap-1.5">
          <span className="eyebrow">Scheduled tasks</span>
          {sched.map((s) => (
            <div key={s.name} className="flex flex-col gap-px rounded-btn border border-border px-2.5 py-1.5">
              <span className="text-xs font-medium text-text">{s.name}</span>
              <span className="text-2xs text-text3">{s.when}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

function dotFor(state: string): string {
  if (/run|active/i.test(state)) return 'var(--green)'
  if (/wait|start|pending/i.test(state)) return 'var(--amber)'
  if (/error|fail/i.test(state)) return 'var(--red)'
  if (/async/i.test(state)) return 'var(--blue)'
  return 'var(--text3)'
}
