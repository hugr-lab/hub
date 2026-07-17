import { cn } from '@/lib/cn'
import type { ChatView, SubAgentNode } from '../frames'
import { statusMeta } from '../frames'

function fmtTokens(n?: number): string {
  if (n == null) return '—'
  return n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n)
}

function ageString(iso?: string): string {
  if (!iso) return ''
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  const s = Math.max(0, Math.round((Date.now() - t) / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.round(s / 60)}m`
  return `${Math.round(s / 3600)}h`
}

function dotFor(state?: string): string {
  const s = state ?? ''
  if (/run|active/i.test(s)) return 'var(--green)'
  if (/wait|start|pending/i.test(s)) return 'var(--amber)'
  if (/error|fail/i.test(s)) return 'var(--red)'
  return 'var(--text3)'
}

function SubAgent({ node }: { node: SubAgentNode }) {
  return (
    <>
      <div
        className="flex items-center gap-2 py-0.5 text-xs"
        style={{ paddingLeft: Math.max(0, node.depth - 1) * 12 }}
      >
        <span className="h-[7px] w-[7px] flex-none rounded-full" style={{ background: dotFor(node.state) }} />
        <span className="min-w-0 flex-1 truncate text-text">{node.role || node.skill || node.id}</span>
        {node.lastTool && <span className="truncate font-mono text-2xs text-text3">{node.lastTool}</span>}
      </div>
      {node.children.map((c) => (
        <SubAgent key={c.id} node={c} />
      ))}
    </>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline">
      <span className="flex-1 text-text3">{label}</span>
      <span className="font-mono text-text2">{value}</span>
    </div>
  )
}

export function LiveViewPanel({ view }: { view: ChatView }) {
  const meta = statusMeta(view.status)
  const live = view.live
  const recap = view.recap
  const ctx = live?.context

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
        {/* recap — what the chat is about */}
        {recap?.text && (
          <div className="flex flex-col gap-1">
            <span className="eyebrow">Recap</span>
            {recap.topic && <span className="text-xs font-semibold text-text">{recap.topic}</span>}
            <p className="text-xs leading-snug text-text2">{recap.text}</p>
          </div>
        )}

        {/* current activity */}
        {live?.lastTool && (
          <div className="flex flex-col gap-1">
            <span className="eyebrow">Now running</span>
            <div className="flex items-center gap-2 text-xs">
              <span
                className="h-[7px] w-[7px] flex-none animate-pulse rounded-full"
                style={{ background: 'var(--green)' }}
              />
              <span className="min-w-0 flex-1 truncate font-mono text-text">{live.lastTool.name}</span>
              <span className="text-2xs text-text3">{ageString(live.lastTool.startedAt)}</span>
            </div>
          </div>
        )}

        {/* context tokens (the engine emits amounts, not a window ceiling) */}
        <div className="flex flex-col gap-1">
          <span className="eyebrow">Context {live?.tier ? `· ${live.tier}` : ''}</span>
          {live ? (
            <div className="flex flex-col gap-0.5 text-xs text-text2">
              <Row label="History" value={fmtTokens(ctx?.historyTokens)} />
              <Row label="Prompt / out" value={`${fmtTokens(ctx?.promptTokens)} / ${fmtTokens(ctx?.completionTokens)}`} />
              <Row label="Tools" value={fmtTokens(ctx?.toolsTokens)} />
              {ctx?.skillsLoadedTokens != null && <Row label="Skills" value={fmtTokens(ctx?.skillsLoadedTokens)} />}
            </div>
          ) : (
            <span className="py-1 text-xs text-text3">waiting for status…</span>
          )}
        </div>

        {/* loaded skills */}
        {live?.skills && (
          <div className="flex flex-col gap-1.5">
            <span className="eyebrow">
              Skills · {live.skills.loaded.length} · {live.skills.tools} tools
            </span>
            <div className="flex flex-wrap gap-1">
              {live.skills.loaded.length === 0 ? (
                <span className="text-xs text-text3">none loaded</span>
              ) : (
                live.skills.loaded.map((s) => (
                  <span
                    key={s}
                    className="rounded-btn border border-border px-1.5 py-0.5 font-mono text-2xs text-text2"
                  >
                    {s}
                  </span>
                ))
              )}
            </div>
          </div>
        )}

        {/* sub-agents */}
        {live && live.children.length > 0 && (
          <div className="flex flex-col gap-1">
            <span className="eyebrow">Sub-agents</span>
            {live.children.map((c) => (
              <SubAgent key={c.id} node={c} />
            ))}
          </div>
        )}

        {/* mission / plan (compact) */}
        {live?.mission && (live.mission.activeWave || live.mission.handoffCount != null) && (
          <div className="flex flex-col gap-0.5 text-xs">
            <span className="eyebrow">Mission</span>
            <span className="text-text2">
              {live.mission.activeWave ? `wave: ${live.mission.activeWave}` : ''}
              {live.mission.handoffCount != null ? ` · ${live.mission.handoffCount} handoffs` : ''}
            </span>
          </div>
        )}
        {live?.plan?.currentStep && (
          <div className="flex flex-col gap-0.5 text-xs">
            <span className="eyebrow">Plan</span>
            <span className="truncate text-text2">→ {live.plan.currentStep}</span>
          </div>
        )}
      </div>
    </div>
  )
}
