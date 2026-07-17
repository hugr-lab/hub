/**
 * Chat frame protocol — the shapes the hugen `/v1` SSE stream emits and a
 * reducer that folds the raw frame log into renderable items. See
 * design/009-management-console/claude-design-prompt.md §"Chat frame protocol".
 */

export interface FrameAuthor {
  id: string
  kind: string
  name?: string
  roles?: string[]
}

export interface Frame {
  frame_id: string
  session_id?: string
  kind: string
  author?: FrameAuthor
  occurred_at?: string
  request_id?: string
  seq?: number
  payload?: Record<string, unknown>
}

export type SessionState =
  | 'idle'
  | 'active'
  | 'wait_subagents'
  | 'wait_approval'
  | 'wait_user_input'
  | 'terminated'

export interface InquiryClarification {
  id: string
  question: string
  kind: 'required' | 'optional' | 'comment'
  options?: string[]
  default?: string
  allow_comment?: boolean
  multi?: boolean
}

export interface Inquiry {
  request_id: string
  type: 'approval' | 'clarification' | 'research_batch'
  question?: string
  context?: string
  options?: string[]
  clarifications?: InquiryClarification[]
  timeout_ms?: number
}

/* ── Folded render items ──────────────────────────────────────────── */

export type RenderItem =
  | { kind: 'user'; id: string; text: string }
  | { kind: 'agent'; id: string; text: string; streaming: boolean; usage?: string }
  | { kind: 'reasoning'; id: string; text: string; streaming: boolean; elapsedMs?: number }
  | {
      kind: 'tool'
      id: string
      toolId: string
      name: string
      args: string
      result?: string
      state: 'running' | 'done' | 'error'
    }
  | { kind: 'artifact'; id: string; name: string; size?: string; artifactId?: string }
  | { kind: 'system'; id: string; text: string; tone?: 'muted' | 'error' }

export interface Artifact {
  id: string
  name: string
  size?: string
  by?: string
  time?: string
}

/** One node of the recursive sub-agent tree (liveview `children`). */
export interface SubAgentNode {
  id: string
  role?: string
  skill?: string
  tier?: string
  depth: number
  state?: string
  lastTool?: string
  children: SubAgentNode[]
}

/**
 * The rich live-view projection, decoded from the single liveview/status
 * extension frame (outbox-only, never replayed). Undefined until the first one
 * arrives (or on reconnect to an idle session).
 */
export interface LiveStatus {
  lifecycle?: string
  tier?: string
  lastTool?: { name: string; startedAt?: string }
  recentActivity: { name: string; startedAt?: string }[]
  /** loaded skill names + total tool count (extensions.skill). */
  skills?: { loaded: string[]; tools: number }
  /** token occupancy — the engine emits amounts, not a window ceiling. */
  context: {
    historyTokens?: number
    promptTokens?: number
    completionTokens?: number
    toolsTokens?: number
    skillsLoadedTokens?: number
    skillsAvailableTokens?: number
    taskTokens?: number
  }
  children: SubAgentNode[]
  plan?: { active?: boolean; currentStep?: string; comments?: number }
  mission?: { activeWave?: string; handoffCount?: number }
}

/** Recap marker (extension `recap`, op `set`): a rolling summary of the chat. */
export interface Recap {
  topic?: string
  text?: string
  categories?: string[]
}

export interface ChatView {
  items: RenderItem[]
  artifacts: Artifact[]
  inquiry: Inquiry | null
  status: SessionState
  statusReason?: string
  /** rich sidebar projection (skills, context, sub-agents); latest snapshot. */
  live?: LiveStatus
  /** rolling recap (topic + theme). */
  recap?: Recap
  lastUsage?: string
}

function str(v: unknown): string {
  if (v == null) return ''
  return typeof v === 'string' ? v : JSON.stringify(v)
}

function fmtUsage(u: unknown): string | undefined {
  if (!u || typeof u !== 'object') return undefined
  const o = u as Record<string, unknown>
  const up = o.input ?? o.prompt ?? o.up
  const down = o.output ?? o.completion ?? o.down
  if (up == null && down == null) return undefined
  return `↑ ${up ?? '?'} · ↓ ${down ?? '?'} tok`
}

type Obj = Record<string, unknown>
const asObj = (v: unknown): Obj => (v && typeof v === 'object' ? (v as Obj) : {})
const num = (v: unknown): number | undefined => (v == null ? undefined : Number(v))

// Decode the recursive liveview `children` map into a sub-agent tree.
function parseChildren(children: unknown, meta: unknown, parentDepth: number): SubAgentNode[] {
  if (!children || typeof children !== 'object') return []
  const cm = asObj(meta)
  return Object.entries(children as Obj).map(([id, raw]) => {
    const c = asObj(raw)
    const m = asObj(cm[id])
    const depth = typeof c.depth === 'number' ? (c.depth as number) : parentDepth + 1
    const lt = asObj(c.last_tool_call)
    return {
      id,
      role: str(m.role || c.role) || undefined,
      skill: str(m.skill || c.skill) || undefined,
      tier: str(c.tier) || undefined,
      depth,
      state: str(c.lifecycle_state) || undefined,
      lastTool: lt.name ? str(lt.name) : undefined,
      children: parseChildren(c.children, c.child_meta, depth),
    }
  })
}

// Decode the liveview/status `data` blob into the LiveStatus projection.
function parseLiveStatus(data: Obj): LiveStatus {
  const exts = asObj(data.extensions)
  const skillExt = exts.skill ? asObj(exts.skill) : undefined
  const cb = asObj(data.context_budget)
  const su = asObj(cb.session_usage)
  const sk = asObj(cb.skills)
  const plan = exts.plan ? asObj(exts.plan) : undefined
  const mission = exts.mission ? asObj(exts.mission) : undefined
  const lt = data.last_tool_call ? asObj(data.last_tool_call) : undefined
  return {
    lifecycle: str(data.lifecycle_state) || undefined,
    tier: str(data.tier) || undefined,
    lastTool: lt ? { name: str(lt.name), startedAt: str(lt.started_at) || undefined } : undefined,
    recentActivity: Array.isArray(data.recent_activity)
      ? (data.recent_activity as unknown[]).map((r) => {
          const o = asObj(r)
          return { name: str(o.name), startedAt: str(o.started_at) || undefined }
        })
      : [],
    skills: skillExt
      ? { loaded: Array.isArray(skillExt.loaded) ? (skillExt.loaded as unknown[]).map(str) : [], tools: Number(skillExt.tools ?? 0) }
      : undefined,
    context: {
      historyTokens: num(cb.history_tokens),
      promptTokens: num(su.prompt_tokens),
      completionTokens: num(su.completion_tokens),
      toolsTokens: num(cb.tools_tokens),
      skillsLoadedTokens: num(sk.loaded_tokens),
      skillsAvailableTokens: num(sk.available_tokens),
      taskTokens: num(sk.task_tokens),
    },
    children: parseChildren(data.children, data.child_meta, typeof data.depth === 'number' ? (data.depth as number) : 0),
    plan: plan
      ? {
          active: plan.Active === true || plan.active === true,
          currentStep: str(plan.CurrentStep || plan.current_step) || undefined,
          comments: Array.isArray(plan.Comments) ? (plan.Comments as unknown[]).length : num(plan.comments),
        }
      : undefined,
    mission: mission
      ? { activeWave: str(mission.active_wave) || undefined, handoffCount: num(mission.handoff_count) }
      : undefined,
  }
}

/**
 * Fold the ordered frame log into a renderable view: dedup agent/reasoning
 * streaming deltas into single bubbles, pair tool_call/tool_result, collect
 * artifacts, surface the pending inquiry, and derive status + the live-view
 * projection (skills, context, sub-agents) + recap.
 */
export function deriveView(frames: Frame[]): ChatView {
  const items: RenderItem[] = []
  const artifacts: Artifact[] = []
  const toolIndex = new Map<string, number>() // toolId → items index
  let inquiry: Inquiry | null = null
  let status: SessionState = 'idle'
  let statusReason: string | undefined
  let live: LiveStatus | undefined
  let recap: Recap | undefined
  let lastUsage: string | undefined

  // Current open streaming items (per session step).
  let agentIdx = -1
  let reasoningIdx = -1
  let reasoningStart: number | undefined

  const answered = new Set<string>()

  // Reasoning precedes the message/tool_call of an iteration; close it as soon
  // as the next thing lands so "Thinking…" doesn't blink after the answer.
  const closeReasoning = () => {
    if (reasoningIdx !== -1) {
      ;(items[reasoningIdx] as Extract<RenderItem, { kind: 'reasoning' }>).streaming = false
      reasoningIdx = -1
    }
  }

  for (const f of frames) {
    const p = f.payload ?? {}
    switch (f.kind) {
      case 'user_message': {
        items.push({ kind: 'user', id: f.frame_id, text: str(p.text) })
        // A new user turn ends any open agent/reasoning steps.
        agentIdx = -1
        reasoningIdx = -1
        break
      }
      case 'agent_message': {
        closeReasoning()
        const consolidated = p.consolidated === true
        const final = p.final === true
        const text = str(p.text)
        if (agentIdx === -1) {
          // A consolidated/final frame with no text is a tool-only iteration —
          // it carries tool_calls, not an assistant message. Don't open an
          // empty bubble for it.
          if ((consolidated || final) && !text) break
          agentIdx = items.length
          items.push({ kind: 'agent', id: f.frame_id, text: '', streaming: true })
        }
        const it = items[agentIdx] as Extract<RenderItem, { kind: 'agent' }>
        if (consolidated || final) {
          it.text = text || it.text
          it.streaming = false
          const usage = fmtUsage(p.usage)
          if (final && usage) {
            it.usage = usage
            lastUsage = usage
          }
          agentIdx = -1 // close this step
        } else {
          // incremental delta
          it.text += text
        }
        break
      }
      case 'reasoning': {
        const final = p.final === true
        const text = str(p.text)
        if (reasoningIdx === -1) {
          reasoningIdx = items.length
          reasoningStart = f.occurred_at ? Date.parse(f.occurred_at) : undefined
          items.push({ kind: 'reasoning', id: f.frame_id, text: '', streaming: true })
        }
        const it = items[reasoningIdx] as Extract<RenderItem, { kind: 'reasoning' }>
        if (final) {
          if (text) it.text = it.text ? it.text : text
          it.streaming = false
          const end = f.occurred_at ? Date.parse(f.occurred_at) : undefined
          if (reasoningStart && end) it.elapsedMs = end - reasoningStart
          reasoningIdx = -1
        } else {
          it.text += text
        }
        break
      }
      case 'tool_call': {
        closeReasoning()
        const toolId = str(p.tool_id) || f.frame_id
        toolIndex.set(toolId, items.length)
        items.push({
          kind: 'tool',
          id: f.frame_id,
          toolId,
          name: str(p.name) || 'tool',
          args: typeof p.args === 'string' ? p.args : JSON.stringify(p.args ?? {}, null, 2),
          state: 'running',
        })
        break
      }
      case 'tool_result': {
        const toolId = str(p.tool_id)
        const idx = toolIndex.get(toolId)
        if (idx != null) {
          const it = items[idx] as Extract<RenderItem, { kind: 'tool' }>
          it.result = typeof p.result === 'string' ? p.result : JSON.stringify(p.result ?? '', null, 2)
          it.state = p.is_error ? 'error' : 'done'
        }
        break
      }
      case 'inquiry_request': {
        const iq = (p.request_id ? p : (p.payload ?? p)) as Record<string, unknown>
        const rid = str(iq.request_id)
        if (rid && !answered.has(rid)) {
          inquiry = {
            request_id: rid,
            type: (iq.type as Inquiry['type']) ?? 'approval',
            question: iq.question ? str(iq.question) : undefined,
            context: iq.context ? str(iq.context) : undefined,
            options: (iq.options as string[]) ?? undefined,
            clarifications: (iq.clarifications as InquiryClarification[]) ?? undefined,
            timeout_ms: (iq.timeout_ms as number) ?? undefined,
          }
        }
        break
      }
      case 'inquiry_answered': {
        const rid = str(p.request_id)
        if (rid) {
          answered.add(rid)
          if (inquiry?.request_id === rid) inquiry = null
        }
        break
      }
      case 'extension_frame': {
        const ext = str(p.extension)
        const op = str(p.op)
        const data = (p.data ?? {}) as Record<string, unknown>
        if (ext === 'artifact' && (op === 'artifact_produced' || op === 'artifact_uploaded')) {
          const name = str(data.name) || str(data.filename) || 'artifact'
          const size = data.size ? str(data.size) : undefined
          const aid = data.id ? str(data.id) : undefined
          items.push({ kind: 'artifact', id: f.frame_id, name, size, artifactId: aid })
          artifacts.push({ id: aid ?? f.frame_id, name, size, by: str(data.by) || 'agent', time: str(data.time) })
        } else if (ext === 'liveview' && op === 'status') {
          // One full snapshot; latest wins. Outbox-only → arrives live, never
          // in replay, so it's undefined until the agent next emits.
          live = parseLiveStatus(data)
          if (live.lifecycle) status = live.lifecycle as SessionState
        } else if (ext === 'recap') {
          recap = {
            topic: str(data.topic) || undefined,
            text: str(data.text) || undefined,
            categories: Array.isArray(data.categories) ? (data.categories as unknown[]).map(str) : undefined,
          }
        }
        break
      }
      case 'system_message':
      case 'system_marker': {
        items.push({ kind: 'system', id: f.frame_id, text: str(p.text) || str(p.message) })
        break
      }
      case 'error': {
        items.push({ kind: 'system', id: f.frame_id, text: str(p.message) || 'Error', tone: 'error' })
        break
      }
      case 'session_status': {
        // Durable lifecycle marker (persists + replays). liveview.lifecycle
        // overrides it when a fresher snapshot is present.
        status = (str(p.state) as SessionState) || status
        statusReason = p.reason ? str(p.reason) : undefined
        const u = fmtUsage(p.usage)
        if (u) lastUsage = u
        break
      }
      case 'session_terminated':
        status = 'terminated'
        break
      default:
        break
    }
  }

  // Cleanup pass. The turn is over (idle/terminated) → nothing is still
  // streaming. Drop closed-empty agent bubbles (tool-only iterations) and stale
  // empty reasoning markers, keeping an empty reasoning ONLY as the live tail
  // "Thinking…" indicator during an active turn.
  const turnOver = status === 'idle' || status === 'terminated'
  const cleaned: RenderItem[] = []
  items.forEach((it, i) => {
    if (turnOver && (it.kind === 'agent' || it.kind === 'reasoning')) it.streaming = false
    if (it.kind === 'agent' && !it.text.trim() && !it.streaming) return
    if (it.kind === 'reasoning' && !it.text.trim()) {
      const liveTail = i === items.length - 1 && !turnOver && it.streaming
      if (!liveTail) return
    }
    cleaned.push(it)
  })

  return { items: cleaned, artifacts, inquiry, status, statusReason, live, recap, lastUsage }
}

/** Status pill label + color token. */
export function statusMeta(state: SessionState): { label: string; color: string; pulse: boolean } {
  switch (state) {
    case 'active':
      return { label: 'Working', color: 'var(--amber)', pulse: true }
    case 'wait_approval':
      return { label: 'Waiting for approval', color: 'var(--amber)', pulse: true }
    case 'wait_subagents':
      return { label: 'Waiting on subagents', color: 'var(--amber)', pulse: true }
    case 'wait_user_input':
      return { label: 'Waiting on you', color: 'var(--amber)', pulse: true }
    case 'terminated':
      return { label: 'Ended', color: 'var(--text3)', pulse: false }
    default:
      return { label: 'Idle', color: 'var(--green)', pulse: false }
  }
}
