import { apiUrl } from './config'
import { authHeader, reportUnauthorized } from './auth-token'

export class RestError extends Error {
  status: number
  body?: string
  constructor(status: number, message: string, body?: string) {
    super(message)
    this.name = 'RestError'
    this.status = status
    this.body = body
  }
}

async function request(path: string, init: RequestInit = {}): Promise<Response> {
  const res = await fetch(apiUrl(path), {
    ...init,
    headers: { ...(init.headers ?? {}), ...(await authHeader()) },
  })
  if (res.status === 401) {
    reportUnauthorized()
    throw new RestError(401, 'Unauthorized')
  }
  return res
}

/** JSON REST call with bearer auth. */
export async function restJSON<T = unknown>(
  path: string,
  init: RequestInit & { json?: unknown } = {},
): Promise<T> {
  const { json, ...rest } = init
  const headers: Record<string, string> = { ...(rest.headers as Record<string, string>) }
  if (json !== undefined) headers['Content-Type'] = 'application/json'
  const res = await request(path, {
    ...rest,
    headers,
    body: json !== undefined ? JSON.stringify(json) : rest.body,
  })
  if (!res.ok) {
    const body = await res.text().catch(() => '')
    throw new RestError(res.status, `HTTP ${res.status}`, body)
  }
  const ct = res.headers.get('content-type') ?? ''
  if (ct.includes('application/json')) return (await res.json()) as T
  return undefined as T
}

/** Raw REST call (for blob downloads, custom handling). */
export function restRaw(path: string, init?: RequestInit): Promise<Response> {
  return request(path, init)
}

/* ── SSE over fetch()+ReadableStream ──────────────────────────────── */

export interface SSEEvent {
  id?: string
  event?: string
  data: string
}

export interface SSEHandlers {
  onEvent: (ev: SSEEvent) => void
  onOpen?: () => void
  onError?: (err: unknown) => void
  /** Called when the connection closes (before a reconnect attempt). */
  onClose?: () => void
}

export interface SSEOptions extends SSEHandlers {
  /** Query params, e.g. `{ live: '1' }`. */
  query?: Record<string, string>
  /** Resume position; updated internally as events arrive. */
  lastEventId?: string
  signal: AbortSignal
  /** Reconnect with backoff on drop (default true). */
  reconnect?: boolean
}

/**
 * Open an SSE stream via fetch (native EventSource can't set Authorization).
 * Parses `id:`/`event:`/`data:` frames, skips `:`-comment heartbeats, resumes
 * with `Last-Event-ID`, and reconnects with backoff until the signal aborts.
 */
export async function openSSE(path: string, opts: SSEOptions): Promise<void> {
  const { signal, reconnect = true } = opts
  let lastEventId = opts.lastEventId
  let backoff = 500

  const qs = opts.query ? '?' + new URLSearchParams(opts.query).toString() : ''

  while (!signal.aborted) {
    try {
      const res = await fetch(apiUrl(path) + qs, {
        method: 'GET',
        signal,
        headers: {
          Accept: 'text/event-stream',
          ...(lastEventId ? { 'Last-Event-ID': lastEventId } : {}),
          ...(await authHeader()),
        },
      })
      if (res.status === 401) {
        reportUnauthorized()
        throw new RestError(401, 'Unauthorized')
      }
      if (!res.ok || !res.body) throw new RestError(res.status, `SSE HTTP ${res.status}`)

      opts.onOpen?.()
      backoff = 500

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buf = ''

      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })

        // Dispatch complete events (blocks separated by a blank line).
        let sep: number
        while ((sep = indexOfDelimiter(buf)) !== -1) {
          const rawEvent = buf.slice(0, sep)
          buf = buf.slice(sep).replace(/^(\r?\n){1,2}/, '')
          const ev = parseEvent(rawEvent)
          if (ev) {
            if (ev.id) lastEventId = ev.id
            opts.onEvent(ev)
          }
        }
      }
      opts.onClose?.()
    } catch (err) {
      if (signal.aborted) return
      opts.onError?.(err)
    }

    if (!reconnect || signal.aborted) return
    await sleep(backoff, signal)
    backoff = Math.min(backoff * 2, 10_000)
  }
}

function indexOfDelimiter(buf: string): number {
  const a = buf.indexOf('\n\n')
  const b = buf.indexOf('\r\n\r\n')
  if (a === -1) return b
  if (b === -1) return a
  return Math.min(a, b)
}

function parseEvent(raw: string): SSEEvent | null {
  let id: string | undefined
  let event: string | undefined
  const dataLines: string[] = []
  for (const line of raw.split(/\r?\n/)) {
    if (line === '' || line.startsWith(':')) continue // comment/heartbeat
    const idx = line.indexOf(':')
    const field = idx === -1 ? line : line.slice(0, idx)
    const value = idx === -1 ? '' : line.slice(idx + 1).replace(/^ /, '')
    if (field === 'id') id = value
    else if (field === 'event') event = value
    else if (field === 'data') dataLines.push(value)
  }
  if (dataLines.length === 0 && id === undefined && event === undefined) return null
  return { id, event, data: dataLines.join('\n') }
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms)
    signal.addEventListener('abort', () => {
      clearTimeout(t)
      resolve()
    }, { once: true })
  })
}
