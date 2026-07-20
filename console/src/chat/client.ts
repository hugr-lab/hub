import type { Frame } from './frames'

export interface Project {
  id: string
  name: string
}

export interface Chat {
  id: string
  name: string
  agent_id: string
  agent_name?: string
  project_id?: string | null
  project_name?: string
  last_active_at?: string
  last?: string
  root_session_id?: string | null
  /** True when the bound session was terminated (dropped, not archived) — the
   *  chat is read-only history and can't be restored to continue. */
  dropped?: boolean
}

export interface PickableAgent {
  id: string
  name: string
  access: string
  status?: string
}

export interface InquiryAnswer {
  request_id: string
  approved?: boolean
  response?: string
  reason?: string
  answers?: Record<string, { value: unknown; comment?: string }>
  auto_approve_tools?: boolean
}

/** One scheduled task owned by a chat's root session (hugen sessionTaskDTO). */
export interface SessionTask {
  id: string
  name: string
  description?: string
  kind: string // wake | spawn
  schedule_kind: string // once_in | once_at | cron | interval
  schedule_spec?: string
  timezone?: string
  status: string // active | paused | cancelled | completed
  pause_reason?: string
  // When a recurring task stops: kind "until_cancel" | "count" | "until";
  // spec holds the count (for count) or an RFC3339 instant (for until).
  end_condition?: { kind: string; spec?: string }
  next_fire?: string
  created_at?: string
}

/** Result of listing a session's scheduled tasks. archivedCount = cancelled +
 *  completed, so a UI can badge/expand history even when the live list is empty. */
export interface SessionTasksResult {
  tasks: SessionTask[]
  archivedCount: number
}

export interface ChatClientOptions {
  apiBase: string
  getToken: () => Promise<string | null> | string | null
  demo?: boolean
}

type FrameListener = (f: Frame) => void

/**
 * Self-contained chat backend client — GraphQL for chats/projects, REST+SSE for
 * the live conversation. Built from injected `apiBase`/`getToken` so the chat
 * microfrontend carries no dependency on the SPA's global config or router.
 * In demo mode it serves mocks and simulates a scripted turn (incl. HITL).
 */
export class ChatClient {
  private apiBase: string
  private getToken: ChatClientOptions['getToken']
  readonly demo: boolean

  constructor(opts: ChatClientOptions) {
    this.apiBase = opts.apiBase ?? ''
    this.getToken = opts.getToken
    this.demo = opts.demo ?? false
  }

  private async headers(extra?: Record<string, string>): Promise<Record<string, string>> {
    const token = await this.getToken()
    return {
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(extra ?? {}),
    }
  }

  private async gql<T>(query: string, variables?: Record<string, unknown>): Promise<T> {
    const res = await fetch(`${this.apiBase}/hugr`, {
      method: 'POST',
      headers: await this.headers({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ query, variables: variables ?? {} }),
    })
    if (!res.ok) throw new Error(`GraphQL HTTP ${res.status}`)
    const body = (await res.json()) as { data?: T; errors?: { message: string }[] }
    if (body.errors?.length) throw new Error(body.errors.map((e) => e.message).join('; '))
    return body.data as T
  }

  /* ── Threads / projects (GraphQL) ──────────────────────────────── */

  async listProjects(): Promise<Project[]> {
    if (this.demo) return DEMO_PROJECTS
    const d = await this.gql<{ hub: { my_projects: Project[] } }>(
      `query { hub { my_projects { id name } } }`,
    )
    return [...d.hub.my_projects].sort((a, b) => a.name.localeCompare(b.name))
  }

  async listChats(
    archived = false,
    cursor?: { beforeActiveAt: string; beforeId: string },
    limit = 200,
  ): Promise<Chat[]> {
    if (this.demo) return this.demoChats()
    // hub.my_chats is a keyset-paginated table function — all args are required
    // (empty string / false = no filter). Field is `title`, not `name`. Pass the
    // last row's (last_active_at, id) back as the cursor for the next page.
    const d = await this.gql<{
      hub: {
        my_chats: {
          id: string
          title: string
          agent_id: string
          project_id: string | null
          last_active_at: string
          root_session_id: string | null
        }[]
      }
    }>(
      `query($args: hub_my_chats_args!) {
        hub { my_chats(args: $args) { id title agent_id project_id last_active_at root_session_id } }
      }`,
      {
        args: {
          limit,
          before_active_at: cursor?.beforeActiveAt ?? '',
          before_id: cursor?.beforeId ?? '',
          project_id: '',
          agent_id: '',
          q: '',
          archived,
        },
      },
    )
    return d.hub.my_chats.map((c) => ({
      id: c.id,
      name: c.title,
      agent_id: c.agent_id,
      project_id: c.project_id,
      last_active_at: c.last_active_at,
      root_session_id: c.root_session_id,
    }))
  }

  /** Statuses of the given sessions (hub.agent.db.sessions), keyed by id. Used to
   *  tell a dropped (terminated) closed chat from an archived (resumable) one. */
  async sessionStatuses(sessionIds: string[]): Promise<Record<string, string>> {
    if (this.demo || sessionIds.length === 0) return {}
    const d = await this.gql<{ hub: { agent: { db: { sessions: { id: string; status: string }[] } } } }>(
      `query($ids: [String!]) {
        hub { agent { db { sessions(filter: { id: { in: $ids } }) { id status } } } }
      }`,
      { ids: sessionIds },
    )
    const out: Record<string, string> = {}
    for (const s of d.hub.agent.db.sessions) out[s.id] = s.status
    return out
  }

  async listPickableAgents(): Promise<PickableAgent[]> {
    if (this.demo) return DEMO_PICKABLE
    // The my_agent_instances view is lean: id / display_name / status /
    // access_role / hugr_role (no separate desired/runtime).
    const d = await this.gql<{
      hub: { my_agent_instances: { id: string; display_name: string; access_role: string; status: string }[] }
    }>(`query { hub { my_agent_instances { id display_name access_role status } } }`)
    return d.hub.my_agent_instances.map((a) => ({
      id: a.id,
      name: a.display_name,
      access: a.access_role,
      status: a.status,
    }))
  }

  async createChat(agentId: string, opts?: { projectId?: string; name?: string }): Promise<Chat> {
    if (this.demo) {
      const agent = DEMO_PICKABLE.find((a) => a.id === agentId)
      const chat: Chat = {
        id: `c_demo_${this.demoSeq++}`,
        name: opts?.name ?? `New chat with ${agent?.name ?? agentId}`,
        agent_id: agentId,
        agent_name: agent?.name,
        project_id: opts?.projectId ?? null,
        last: 'now',
      }
      this.demoChatList.unshift(chat)
      return chat
    }
    // create_chat args are all String (NON_NULL): project_id='' = no project,
    // title='' = server default. Returns hub_chat (field is `title`, not `name`).
    const d = await this.gql<{
      function: {
        hub: {
          create_chat: { id: string; title: string; agent_id: string; project_id: string | null }
        }
      }
    }>(
      `mutation($a:String!,$p:String!,$t:String!){ function { hub {
        create_chat(agent_id:$a, project_id:$p, title:$t) { id title agent_id project_id }
      } } }`,
      { a: agentId, p: opts?.projectId ?? '', t: opts?.name ?? '' },
    )
    const c = d.function.hub.create_chat
    return { id: c.id, name: c.title, agent_id: c.agent_id, project_id: c.project_id }
  }

  async updateChat(id: string, patch: { name?: string; project_id?: string | null; archived?: boolean }): Promise<void> {
    if (this.demo) return
    // All args String (NON_NULL): '' = unchanged; project_id='none' clears it;
    // archived accepts 'true'/'false' ('' = unchanged). Returns hub_chat.
    await this.gql(
      `mutation($id:String!,$t:String!,$p:String!,$ar:String!){ function { hub {
        update_chat(id:$id, title:$t, project_id:$p, archived:$ar) { id }
      } } }`,
      {
        id,
        t: patch.name ?? '',
        p: patch.project_id === null ? 'none' : patch.project_id ?? '',
        ar: patch.archived === undefined ? '' : String(patch.archived),
      },
    )
  }

  async deleteChat(id: string): Promise<void> {
    if (this.demo) {
      this.demoChatList = this.demoChatList.filter((c) => c.id !== id)
      return
    }
    // delete_chat returns hub_deleted_row { id, deleted }.
    await this.gql(`mutation($id:String!){ function { hub { delete_chat(id:$id){ id deleted } } } }`, { id })
  }

  /* ── Live conversation (REST + SSE) ────────────────────────────── */

  async postMessage(chatId: string, text: string): Promise<{ status: string; session_id?: string }> {
    if (this.demo) {
      this.demoSend(chatId, text)
      return { status: 'accepted' }
    }
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/messages`, {
      method: 'POST',
      headers: await this.headers({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ text }),
    })
    if (!res.ok) throw new Error(`send HTTP ${res.status}`)
    return res.json()
  }

  /**
   * Seed frames to show before the live stream connects. In demo we replay the
   * in-memory log (the demo stream only emits NEW frames). In real mode the SSE
   * stream itself replays the persisted backlog as frames (`openStream` connects
   * in replay mode — no `?live=1`), so nothing is pre-loaded here: the `/events`
   * REST endpoint returns raw `EventRow`s (scroll-back/inspection shape), not the
   * `Frame`s the conversation view folds.
   */
  async getEvents(chatId: string): Promise<Frame[]> {
    if (this.demo) return [...(this.demoFrames.get(chatId) ?? [])]
    return []
  }

  async answerInquiry(chatId: string, answer: InquiryAnswer): Promise<void> {
    if (this.demo) {
      this.demoAnswer(chatId, answer)
      return
    }
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/inquiry`, {
      method: 'POST',
      headers: await this.headers({ 'Content-Type': 'application/json' }),
      body: JSON.stringify(answer),
    })
    if (!res.ok) throw new Error(`inquiry HTTP ${res.status}`)
  }

  async cancelTurn(chatId: string, opts?: { reason?: string; cascade?: boolean }): Promise<void> {
    if (this.demo) {
      this.demoCancel(chatId)
      return
    }
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/cancel`, {
      method: 'POST',
      headers: await this.headers({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ reason: opts?.reason, cascade: opts?.cascade ?? false }),
    })
    if (!res.ok) throw new Error(`cancel HTTP ${res.status}`)
  }

  async listArtifacts(chatId: string): Promise<{ id: string; name: string; size?: string; by?: string; time?: string }[]> {
    if (this.demo) return this.demoArtifacts.get(chatId) ?? []
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/artifacts`, { headers: await this.headers() })
    if (!res.ok) throw new Error(`artifacts HTTP ${res.status}`)
    const body = (await res.json()) as { artifacts?: unknown[] } | unknown[]
    const list = (Array.isArray(body) ? body : (body.artifacts ?? [])) as Record<string, unknown>[]
    // hugen returns protocol.ArtifactRef { id, name, mime, size, created_at }.
    return list.map((a) => ({
      id: String(a.id ?? a.name),
      name: String(a.name ?? a.filename ?? 'artifact'),
      size: a.size ? String(a.size) : undefined,
      by: a.by ? String(a.by) : undefined,
      time: a.created_at ? String(a.created_at) : a.time ? String(a.time) : undefined,
    }))
  }

  /** Scheduled tasks owned by this chat's root session. scope 'live' (default)
   *  returns active + paused only (server-filtered — cheap for long histories);
   *  'all' returns every status incl. cancelled / completed. Empty when the
   *  agent has no scheduler wired or no matching tasks. */
  async getTasks(chatId: string, scope: 'live' | 'all' = 'live'): Promise<SessionTasksResult> {
    if (this.demo) return { tasks: [], archivedCount: 0 }
    const qs = scope === 'all' ? '?status=all' : ''
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/tasks${qs}`, { headers: await this.headers() })
    if (!res.ok) throw new Error(`tasks HTTP ${res.status}`)
    const body = (await res.json()) as
      | SessionTask[]
      | { tasks?: SessionTask[]; archived_count?: number }
    if (Array.isArray(body)) return { tasks: body, archivedCount: 0 }
    return { tasks: body.tasks ?? [], archivedCount: body.archived_count ?? 0 }
  }

  /** Archive a chat: cancels its session's in-flight work (cascade) but keeps
   *  the session resumable — restore revives it fully. Reversible. */
  async archiveChat(chatId: string): Promise<void> {
    if (this.demo) return
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/archive`, {
      method: 'POST',
      headers: await this.headers(),
    })
    if (!res.ok) throw new Error(`archive chat HTTP ${res.status}`)
  }

  /** Drop a chat: /end — terminates the session (keeps history but it can't be
   *  revived) and archives. Restore shows read-only history. Not reversible. */
  async dropChat(chatId: string): Promise<void> {
    if (this.demo) return
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/drop`, {
      method: 'POST',
      headers: await this.headers(),
    })
    if (!res.ok) throw new Error(`drop chat HTTP ${res.status}`)
  }

  /** Cancel a scheduled task (stops it firing, keeps the row as history). */
  async cancelTask(chatId: string, taskId: string): Promise<void> {
    if (this.demo) return
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/tasks/${taskId}/cancel`, {
      method: 'POST',
      headers: await this.headers(),
    })
    if (!res.ok) throw new Error(`cancel task HTTP ${res.status}`)
  }

  /** Delete a scheduled task and its schedule entirely. */
  async deleteTask(chatId: string, taskId: string): Promise<void> {
    if (this.demo) return
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/tasks/${taskId}`, {
      method: 'DELETE',
      headers: await this.headers(),
    })
    if (!res.ok) throw new Error(`delete task HTTP ${res.status}`)
  }

  /** Pause ('pause') or resume ('resume') a scheduled task. */
  async setTaskPaused(chatId: string, taskId: string, action: 'pause' | 'resume'): Promise<void> {
    if (this.demo) return
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/tasks/${taskId}/${action}`, {
      method: 'POST',
      headers: await this.headers(),
    })
    if (!res.ok) throw new Error(`${action} task HTTP ${res.status}`)
  }

  async uploadArtifact(chatId: string, file: File): Promise<void> {
    if (this.demo) return
    // hugen ingests the RAW request body (not multipart) with the display name
    // in ?name=. The gateway forwards both body and query verbatim.
    const res = await fetch(
      `${this.apiBase}/api/v1/chats/${chatId}/artifacts?name=${encodeURIComponent(file.name)}`,
      {
        method: 'POST',
        headers: await this.headers({ 'Content-Type': file.type || 'application/octet-stream' }),
        body: file,
      },
    )
    if (!res.ok) throw new Error(`upload HTTP ${res.status}`)
  }

  async downloadArtifact(chatId: string, artifactId: string, filename: string): Promise<void> {
    if (this.demo) return
    const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/artifacts/${artifactId}`, {
      headers: await this.headers(),
    })
    if (!res.ok) throw new Error(`download HTTP ${res.status}`)
    const blob = await res.blob()
    triggerDownload(blob, filename)
  }

  /**
   * Open the SSE frame stream via fetch (native EventSource can't set
   * Authorization). Parses id/data frames, skips heartbeats, resumes with
   * Last-Event-ID, reconnects with backoff until the signal aborts.
   */
  async openStream(
    chatId: string,
    handlers: { onFrame: (f: Frame) => void; onOpen?: () => void; onError?: (e: unknown) => void; signal: AbortSignal; lastEventId?: string },
  ): Promise<void> {
    if (this.demo) {
      const listener: FrameListener = handlers.onFrame
      let set = this.demoListeners.get(chatId)
      if (!set) {
        set = new Set()
        this.demoListeners.set(chatId, set)
      }
      set.add(listener)
      handlers.onOpen?.()
      handlers.signal.addEventListener('abort', () => set!.delete(listener), { once: true })
      return
    }

    const { signal } = handlers
    let lastEventId = handlers.lastEventId
    let backoff = 500
    while (!signal.aborted) {
      try {
        // No ?live=1 — replay the persisted backlog (as frames, after any
        // Last-Event-ID cursor) then go live. `live=1` is the A2A-bridge mode
        // that suppresses history; the console wants history + live.
        const res = await fetch(`${this.apiBase}/api/v1/chats/${chatId}/stream`, {
          headers: await this.headers({
            Accept: 'text/event-stream',
            ...(lastEventId ? { 'Last-Event-ID': lastEventId } : {}),
          }),
          signal,
        })
        if (!res.ok || !res.body) throw new Error(`stream HTTP ${res.status}`)
        handlers.onOpen?.()
        backoff = 500
        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buf = ''
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break
          buf += decoder.decode(value, { stream: true })
          let sep: number
          while ((sep = delimiter(buf)) !== -1) {
            const raw = buf.slice(0, sep)
            buf = buf.slice(sep).replace(/^(\r?\n){1,2}/, '')
            const parsed = parseEvent(raw)
            if (parsed) {
              if (parsed.id) lastEventId = parsed.id
              try {
                const frame = JSON.parse(parsed.data) as Frame
                if (parsed.id && frame.seq == null) frame.seq = Number(parsed.id)
                handlers.onFrame(frame)
              } catch {
                /* skip malformed frame */
              }
            }
          }
        }
      } catch (err) {
        if (signal.aborted) return
        handlers.onError?.(err)
      }
      if (signal.aborted) return
      await delay(backoff, signal)
      backoff = Math.min(backoff * 2, 10_000)
    }
  }

  /* ── Demo simulation ───────────────────────────────────────────── */

  private demoSeq = 1
  private demoFrameSeq = 1
  private demoChatList: Chat[] = [...DEMO_CHATS]
  private demoFrames = new Map<string, Frame[]>()
  private demoListeners = new Map<string, Set<FrameListener>>()
  private demoArtifacts = new Map<string, { id: string; name: string; size?: string; by?: string; time?: string }[]>()
  private demoPending = new Map<string, { toolThenFinish: () => void }>()
  private demoTimers = new Map<string, ReturnType<typeof setTimeout>[]>()

  private demoChats(): Chat[] {
    return this.demoChatList
  }

  private emit(chatId: string, partial: Partial<Frame> & { kind: string }): void {
    const frame: Frame = {
      frame_id: `f_${this.demoFrameSeq}`,
      seq: this.demoFrameSeq,
      occurred_at: new Date().toISOString(),
      ...partial,
    }
    this.demoFrameSeq++
    const arr = this.demoFrames.get(chatId) ?? []
    arr.push(frame)
    this.demoFrames.set(chatId, arr)
    this.demoListeners.get(chatId)?.forEach((l) => l(frame))
  }

  private schedule(chatId: string, ms: number, fn: () => void): void {
    const t = setTimeout(fn, ms)
    const arr = this.demoTimers.get(chatId) ?? []
    arr.push(t)
    this.demoTimers.set(chatId, arr)
  }

  private clearTimers(chatId: string): void {
    this.demoTimers.get(chatId)?.forEach(clearTimeout)
    this.demoTimers.set(chatId, [])
  }

  private demoSend(chatId: string, text: string): void {
    this.emit(chatId, { kind: 'user_message', payload: { text } })
    this.emit(chatId, { kind: 'session_status', payload: { state: 'active' } })
    // reasoning stream
    const think = ['Let me look at the request. ', 'I should query the gateway logs ', 'and check the 24h window.']
    think.forEach((t, i) =>
      this.schedule(chatId, 250 + i * 260, () => this.emit(chatId, { kind: 'reasoning', payload: { text: t, chunk_seq: i } })),
    )
    this.schedule(chatId, 1100, () => this.emit(chatId, { kind: 'reasoning', payload: { final: true } }))
    // tool call
    this.schedule(chatId, 1300, () =>
      this.emit(chatId, { kind: 'tool_call', payload: { tool_id: 't1', name: 'hugr_query', args: { query: 'SELECT count(*) FROM gateway_events WHERE ts > now() - interval 24 hour' } } }),
    )
    this.schedule(chatId, 2100, () =>
      this.emit(chatId, { kind: 'tool_result', payload: { tool_id: 't1', result: '{ "count": 18242 }' } }),
    )
    // approval inquiry
    this.schedule(chatId, 2500, () => {
      this.emit(chatId, {
        kind: 'inquiry_request',
        request_id: 'req_demo_1',
        payload: {
          request_id: 'req_demo_1',
          type: 'approval',
          question: 'Post the incident summary to the #ops Slack webhook?',
          context: 'POST https://hooks.slack.example/… · body: gateway offline 24h summary (2 attachments)',
        },
      })
      this.emit(chatId, { kind: 'session_status', payload: { state: 'wait_approval' } })
    })
    // remember how to finish once approved
    this.demoPending.set(chatId, {
      toolThenFinish: () => {
        this.emit(chatId, { kind: 'tool_call', payload: { tool_id: 't2', name: 'http_post', args: { url: 'https://hooks.slack.example/…' } } })
        this.schedule(chatId, 600, () => this.emit(chatId, { kind: 'tool_result', payload: { tool_id: 't2', result: '{ "ok": true }' } }))
        this.schedule(chatId, 900, () => {
          const name = 'gateway_offline_24h.csv'
          this.emit(chatId, { kind: 'extension_frame', payload: { extension: 'artifact', op: 'artifact_produced', data: { id: 'a_demo_1', name, size: '4.2 KB', by: 'analytics-copilot' } } })
          const list = this.demoArtifacts.get(chatId) ?? []
          list.unshift({ id: 'a_demo_1', name, size: '4.2 KB', by: 'analytics-copilot', time: 'now' })
          this.demoArtifacts.set(chatId, list)
        })
        const reply = 'Done — 18,242 gateway events in the last 24h, offline window 02:10–03:35 UTC. I posted the summary to #ops and saved the raw rows as an artifact.'
        const chunks = reply.match(/.{1,24}/g) ?? [reply]
        chunks.forEach((c, i) => this.schedule(chatId, 1200 + i * 90, () => this.emit(chatId, { kind: 'agent_message', payload: { text: c, consolidated: false, chunk_seq: i } })))
        this.schedule(chatId, 1200 + chunks.length * 90 + 120, () => {
          this.emit(chatId, { kind: 'agent_message', payload: { text: reply, consolidated: true } })
          this.emit(chatId, { kind: 'agent_message', payload: { text: reply, final: true, usage: { input: 3882, output: 2405 } } })
          this.emit(chatId, { kind: 'session_status', payload: { state: 'idle' } })
        })
      },
    })
  }

  private demoAnswer(chatId: string, answer: InquiryAnswer): void {
    this.emit(chatId, { kind: 'inquiry_answered', payload: { request_id: answer.request_id } })
    const pending = this.demoPending.get(chatId)
    if (answer.approved) {
      this.emit(chatId, { kind: 'session_status', payload: { state: 'active' } })
      pending?.toolThenFinish()
    } else {
      this.emit(chatId, { kind: 'system_message', payload: { text: 'Approval rejected — the agent skipped the Slack post.' } })
      this.emit(chatId, { kind: 'agent_message', payload: { text: 'Understood — I did not post to Slack. The raw analysis is still available if you want it.', final: true, usage: { input: 3882, output: 640 } } })
      this.emit(chatId, { kind: 'session_status', payload: { state: 'idle' } })
    }
    this.demoPending.delete(chatId)
  }

  private demoCancel(chatId: string): void {
    this.clearTimers(chatId)
    this.demoPending.delete(chatId)
    this.emit(chatId, { kind: 'system_message', payload: { text: 'Turn cancelled by you.' } })
    this.emit(chatId, { kind: 'session_status', payload: { state: 'idle' } })
  }
}

/* ── SSE parsing helpers ──────────────────────────────────────────── */

function delimiter(buf: string): number {
  const a = buf.indexOf('\n\n')
  const b = buf.indexOf('\r\n\r\n')
  if (a === -1) return b
  if (b === -1) return a
  return Math.min(a, b)
}

function parseEvent(raw: string): { id?: string; data: string } | null {
  let id: string | undefined
  const data: string[] = []
  for (const line of raw.split(/\r?\n/)) {
    if (line === '' || line.startsWith(':')) continue
    const idx = line.indexOf(':')
    const field = idx === -1 ? line : line.slice(0, idx)
    const value = idx === -1 ? '' : line.slice(idx + 1).replace(/^ /, '')
    if (field === 'id') id = value
    else if (field === 'data') data.push(value)
  }
  if (data.length === 0) return null
  return { id, data: data.join('\n') }
}

function delay(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms)
    signal.addEventListener('abort', () => { clearTimeout(t); resolve() }, { once: true })
  })
}

function triggerDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/* ── Demo seed data ───────────────────────────────────────────────── */

const DEMO_PROJECTS: Project[] = [
  { id: 'p_ops', name: 'Ops & Incidents' },
  { id: 'p_rev', name: 'Revenue' },
]

const DEMO_PICKABLE: PickableAgent[] = [
  { id: 'ag_analytics', name: 'analytics-copilot', access: 'owner', status: 'running' },
  { id: 'ag_finance', name: 'finance-qa', access: 'member', status: 'running' },
  { id: 'ag_geo', name: 'geo-research', access: 'owner', status: 'paused' },
]

const DEMO_CHATS: Chat[] = [
  { id: 'c1', name: 'Gateway outage triage', agent_id: 'ag_analytics', agent_name: 'analytics-copilot', project_id: 'p_ops', last: '2m' },
  { id: 'c2', name: 'Q3 revenue variance', agent_id: 'ag_finance', agent_name: 'finance-qa', project_id: 'p_rev', last: '1h' },
  { id: 'c3', name: 'Store coverage by region', agent_id: 'ag_geo', agent_name: 'geo-research', project_id: null, last: 'Mon' },
]
