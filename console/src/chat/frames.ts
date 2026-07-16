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

export interface MissionRow {
  id: string
  name: string
  state: string
  depth: number
  async?: boolean
}

export interface ChatView {
  items: RenderItem[]
  artifacts: Artifact[]
  inquiry: Inquiry | null
  status: SessionState
  statusReason?: string
  missions: MissionRow[]
  /** context budget e.g. { used: 84100, limit: 200000 } */
  budget?: { used: number; limit: number }
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

/**
 * Fold the ordered frame log into a renderable view: dedup agent/reasoning
 * streaming deltas into single bubbles, pair tool_call/tool_result, collect
 * artifacts, surface the pending inquiry, and derive status + budget.
 */
export function deriveView(frames: Frame[]): ChatView {
  const items: RenderItem[] = []
  const artifacts: Artifact[] = []
  const toolIndex = new Map<string, number>() // toolId → items index
  let inquiry: Inquiry | null = null
  let status: SessionState = 'idle'
  let statusReason: string | undefined
  let missions: MissionRow[] = []
  let budget: ChatView['budget']
  let lastUsage: string | undefined

  // Current open streaming items (per session step).
  let agentIdx = -1
  let reasoningIdx = -1
  let reasoningStart: number | undefined

  const answered = new Set<string>()

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
        const consolidated = p.consolidated === true
        const final = p.final === true
        const text = str(p.text)
        if (agentIdx === -1) {
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
          const rows = (data.missions ?? data.subagents) as unknown[] | undefined
          if (Array.isArray(rows)) {
            missions = rows.map((r, i) => {
              const o = r as Record<string, unknown>
              return {
                id: str(o.id) || String(i),
                name: str(o.name),
                state: str(o.state),
                depth: typeof o.depth === 'number' ? o.depth : 0,
                async: o.async === true,
              }
            })
          }
          if (data.budget && typeof data.budget === 'object') {
            const b = data.budget as Record<string, unknown>
            budget = { used: Number(b.used ?? 0), limit: Number(b.limit ?? 0) }
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
        status = (str(p.state) as SessionState) || status
        statusReason = p.reason ? str(p.reason) : undefined
        const u = fmtUsage(p.usage)
        if (u) lastUsage = u
        if (p.budget && typeof p.budget === 'object') {
          const b = p.budget as Record<string, unknown>
          budget = { used: Number(b.used ?? 0), limit: Number(b.limit ?? 0) }
        }
        break
      }
      case 'session_terminated':
        status = 'terminated'
        break
      default:
        break
    }
  }

  return { items, artifacts, inquiry, status, statusReason, missions, budget, lastUsage }
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
