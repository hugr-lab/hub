import { useState } from 'react'
import { cn } from '@/lib/cn'
import type { SessionTask } from '../client'
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

// untilString formats a FUTURE instant as "in 3m" / "in 2h" / "due".
function untilString(iso?: string): string {
  if (!iso) return ''
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  const s = Math.round((t - Date.now()) / 1000)
  if (s <= 0) return 'due'
  if (s < 60) return `in ${s}s`
  if (s < 3600) return `in ${Math.round(s / 60)}m`
  if (s < 86400) return `in ${Math.round(s / 3600)}h`
  return `in ${Math.round(s / 86400)}d`
}

function taskDot(status: string): string {
  if (/active/i.test(status)) return 'var(--green)'
  if (/paused/i.test(status)) return 'var(--amber)'
  return 'var(--text3)' // cancelled | completed
}

// endConditionLabel renders when a recurring task stops: "×5" (5 fires),
// "until Jul 25" (deadline), or "" for until_cancel (runs until cancelled).
function endConditionLabel(ec?: { kind: string; spec?: string }): string {
  if (!ec) return ''
  if (ec.kind === 'count' && ec.spec) return `×${ec.spec}`
  if (ec.kind === 'until' && ec.spec) {
    const d = new Date(ec.spec)
    return Number.isNaN(+d) ? `until ${ec.spec}` : `until ${d.toLocaleDateString()}`
  }
  return ''
}

// ScheduleRow renders one task with hover-revealed actions: pause/resume
// (reversible, instant) and cancel (destructive → two-step inline confirm, no
// blocking dialog). Actions show only for live (active/paused) tasks.
function ScheduleRow({
  task,
  onCancel,
  onSetPaused,
}: {
  task: SessionTask
  onCancel?: (id: string) => Promise<void>
  onSetPaused?: (id: string, action: 'pause' | 'resume') => Promise<void>
}) {
  const [confirmingCancel, setConfirmingCancel] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState(false)
  const end = endConditionLabel(task.end_condition)
  const isActive = task.status === 'active'
  const isPaused = task.status === 'paused'
  const isLive = isActive || isPaused

  const act = async (fn: () => Promise<void>) => {
    setBusy(true)
    setErr(false)
    try {
      await fn()
      setConfirmingCancel(false)
    } catch {
      setErr(true)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="group flex flex-col gap-0.5">
      <div className="flex items-center gap-2 text-xs">
        <span className="h-[7px] w-[7px] flex-none rounded-full" style={{ background: taskDot(task.status) }} />
        <span className="min-w-0 flex-1 truncate text-text">{task.name || '(unnamed)'}</span>
        {isActive && task.next_fire && (
          <span className="flex-none text-2xs text-text3">{untilString(task.next_fire)}</span>
        )}
      </div>
      <div className="flex items-center gap-1.5 pl-[15px]">
        <span className="min-w-0 flex-1 truncate font-mono text-2xs text-text3">
          {task.schedule_kind}
          {task.schedule_spec ? ` · ${task.schedule_spec}` : ''}
          {end ? ` · ${end}` : ''}
          {!isActive ? ` · ${task.pause_reason || task.status}` : ''}
        </span>
        {confirmingCancel ? (
          <span className="flex flex-none items-center gap-1 text-2xs">
            <span className="text-text3">{err ? 'failed —' : 'cancel?'}</span>
            <button
              className="font-semibold text-red hover:underline disabled:opacity-50"
              disabled={busy}
              onClick={() => onCancel && act(() => onCancel(task.id))}
            >
              {busy ? '…' : 'yes'}
            </button>
            <button className="text-text3 hover:underline" onClick={() => setConfirmingCancel(false)}>
              no
            </button>
          </span>
        ) : (
          isLive && (
            <span className="flex flex-none items-center gap-1.5 text-2xs opacity-0 transition-opacity group-hover:opacity-100">
              {err && <span className="text-red">failed</span>}
              {isActive && onSetPaused && (
                <button
                  className="text-text3 hover:text-amber disabled:opacity-50"
                  disabled={busy}
                  onClick={() => act(() => onSetPaused(task.id, 'pause'))}
                >
                  pause
                </button>
              )}
              {isPaused && onSetPaused && (
                <button
                  className="text-text3 hover:text-green disabled:opacity-50"
                  disabled={busy}
                  onClick={() => act(() => onSetPaused(task.id, 'resume'))}
                >
                  resume
                </button>
              )}
              {onCancel && (
                <button className="text-text3 hover:text-red" onClick={() => setConfirmingCancel(true)}>
                  cancel
                </button>
              )}
            </span>
          )
        )}
      </div>
    </div>
  )
}

function SubAgent({ node }: { node: SubAgentNode }) {
  return (
    <>
      <div className="flex flex-col gap-0.5 py-0.5" style={{ paddingLeft: Math.max(0, node.depth - 1) * 12 }}>
        <div className="flex items-center gap-2 text-xs">
          <span className="h-[7px] w-[7px] flex-none rounded-full" style={{ background: dotFor(node.state) }} />
          <span className="min-w-0 flex-1 truncate text-text">{node.role || node.skill || node.id}</span>
        </div>
        {/* last tool on its own line so a long name never crowds out the role */}
        {node.lastTool && (
          <span className="truncate pl-[15px] font-mono text-2xs text-text3">{node.lastTool}</span>
        )}
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

export function LiveViewPanel({
  view,
  tasks = [],
  archivedTaskCount = 0,
  scheduleScope = 'live',
  onToggleScheduleScope,
  onCancelTask,
  onSetTaskPaused,
}: {
  view: ChatView
  tasks?: SessionTask[]
  archivedTaskCount?: number
  scheduleScope?: 'live' | 'all'
  onToggleScheduleScope?: () => void
  onCancelTask?: (id: string) => Promise<void>
  onSetTaskPaused?: (id: string, action: 'pause' | 'resume') => Promise<void>
}) {
  const meta = statusMeta(view.status)
  const live = view.live
  const recap = view.recap
  const ctx = live?.context
  // last_tool_call is the LAST tool the agent started — it's "running now" only
  // while the session is active; otherwise it's the last completed tool.
  const running = !!live && /active|wait/i.test(live.lifecycle ?? '')

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

        {/* current activity — running now only while active, else last tool */}
        {live?.lastTool && (
          <div className="flex flex-col gap-1">
            <span className="eyebrow">{running ? 'Now running' : 'Last tool'}</span>
            <div className="flex items-center gap-2 text-xs">
              <span
                className={cn('h-[7px] w-[7px] flex-none rounded-full', running && 'animate-pulse')}
                style={{ background: running ? 'var(--green)' : 'var(--text3)' }}
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

        {/* scheduled tasks owned by this session (from the scheduler store).
            Server-filtered: 'live' shows active + paused, 'all' adds archived
            (cancelled / completed). Toggle refetches — no client-side filter. */}
        {(tasks.length > 0 || archivedTaskCount > 0) && (
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center gap-2">
              <span className="eyebrow flex-1">
                Schedules{scheduleScope === 'all' ? ' · all' : ''} · {tasks.length}
              </span>
              {(scheduleScope === 'all' || archivedTaskCount > 0) && onToggleScheduleScope && (
                <button
                  className="text-2xs font-semibold text-accent hover:underline"
                  onClick={onToggleScheduleScope}
                >
                  {scheduleScope === 'all' ? 'active only' : `show all (${archivedTaskCount} archived)`}
                </button>
              )}
            </div>
            {tasks.length === 0 ? (
              <span className="text-2xs text-text3">
                no live schedules{archivedTaskCount > 0 ? ` · ${archivedTaskCount} archived` : ''}
              </span>
            ) : (
              tasks.map((t) => (
                <ScheduleRow key={t.id} task={t} onCancel={onCancelTask} onSetPaused={onSetTaskPaused} />
              ))
            )}
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
