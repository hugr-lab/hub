import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { markChatRead } from '@/api/notifications'
import type { ChatClient, InquiryAnswer, SessionTask } from './client'
import { deriveView, type Artifact, type Frame } from './frames'

// The liveview snapshot is outbox-only (never replayed), so switching away and
// back to a chat would lose the sidebar until the next live emit. Keep the last
// liveview (+ recap) per chat across switches and seed a re-opened chat with
// them, so its live view is restored immediately. Module-level → survives the
// hook remount that a chat switch triggers.
const stickyFrames = new Map<string, { liveview?: Frame; recap?: Frame }>()

function stickyExt(f: Frame): 'liveview' | 'recap' | null {
  const p = f.payload as Record<string, unknown> | undefined
  if (f.kind !== 'extension_frame' || !p) return null
  const ext = p.extension
  return ext === 'liveview' || ext === 'recap' ? ext : null
}

function cacheSticky(chatId: string, f: Frame) {
  const ext = stickyExt(f)
  if (!ext) return
  const entry = stickyFrames.get(chatId) ?? {}
  entry[ext] = f
  stickyFrames.set(chatId, entry)
}

function seedFrames(chatId: string | null): Frame[] {
  if (!chatId) return []
  const e = stickyFrames.get(chatId)
  if (!e) return []
  return [e.recap, e.liveview].filter(Boolean) as Frame[]
}

// Inquiries the user answered locally, keyed by chat. The confirming signal
// (inquiry_answered / a cleared-pending liveview) is outbox-only and may never
// arrive — the session often goes idle right after answering, and idle emits no
// status. Without persisting the answer, switching away and back re-derives the
// modal from the STALE sticky liveview (which still carries pending_inquiry) and
// re-opens an already-answered approval. Module-level → survives the chat-switch
// remount, so an answered inquiry stays closed across switches.
const answeredByChat = new Map<string, Set<string>>()

function markAnswered(chatId: string, rid: string) {
  const s = answeredByChat.get(chatId) ?? new Set<string>()
  s.add(rid)
  answeredByChat.set(chatId, s)
}

/**
 * Drives one chat conversation: backfills persisted frames, subscribes to the
 * live SSE stream, folds frames into a render view, and exposes turn actions.
 */
export function useChat(client: ChatClient, chatId: string | null) {
  const [frames, setFrames] = useState<Frame[]>([])
  const [connected, setConnected] = useState(false)
  // unreachable = the stream errored before it ever connected — the agent is
  // almost certainly stopped. Distinguishes "can't reach it" (show a hint) from
  // "still connecting" (show the loader) so a stopped agent's chat doesn't hang
  // on an endless spinner. everConnected gates it so a mid-session blip (which
  // auto-reconnects) doesn't flip to the error state.
  const [unreachable, setUnreachable] = useState(false)
  const everConnected = useRef(false)
  // Mirrors `connected` for imperative reads (sendMessage) without adding it to
  // effect deps. A `reconnect` bump re-runs the connect effect immediately —
  // used to skip the backoff wait when the first message warms a cold session.
  const connectedRef = useRef(false)
  const [reconnect, setReconnect] = useState(0)
  const [loadedArtifacts, setLoadedArtifacts] = useState<Artifact[]>([])
  const [tasks, setTasks] = useState<SessionTask[]>([])
  const [archivedTaskCount, setArchivedTaskCount] = useState(0)
  const [scheduleScope, setScheduleScope] = useState<'live' | 'all'>('live')
  const [answeredIds, setAnsweredIds] = useState<Set<string>>(new Set())
  const seen = useRef<Set<string>>(new Set())
  const qc = useQueryClient()

  const append = useCallback((incoming: Frame[]) => {
    setFrames((prev) => {
      const add = incoming.filter((f) => {
        const key = f.frame_id ?? String(f.seq)
        if (seen.current.has(key)) return false
        seen.current.add(key)
        return true
      })
      return add.length ? [...prev, ...add] : prev
    })
  }, [])

  // (re)connect whenever the chat changes.
  useEffect(() => {
    // Restore locally-answered inquiries for this chat so a switch-back doesn't
    // re-open an already-answered approval from the stale sticky liveview.
    setAnsweredIds(new Set(chatId ? answeredByChat.get(chatId) : undefined))
    setScheduleScope('live') // each chat opens on the live-schedules view
    if (!chatId) {
      setFrames([])
      seen.current = new Set()
      return
    }
    // Seed from the sticky cache so the last-known live view is shown instantly
    // on switch-back (before the stream replay / next live emit).
    const seed = seedFrames(chatId)
    seen.current = new Set(seed.map((f) => f.frame_id ?? String(f.seq)))
    setFrames(seed)
    setConnected(false)
    setUnreachable(false)
    everConnected.current = false
    connectedRef.current = false
    const ac = new AbortController()

    ;(async () => {
      try {
        const backfill = await client.getEvents(chatId)
        append(backfill)
      } catch {
        /* stream will still deliver live frames */
      }
      await client.openStream(chatId, {
        signal: ac.signal,
        onOpen: () => {
          everConnected.current = true
          connectedRef.current = true
          setConnected(true)
          setUnreachable(false)
        },
        onError: () => {
          connectedRef.current = false
          setConnected(false)
          if (!everConnected.current) setUnreachable(true)
        },
        onFrame: (f) => {
          cacheSticky(chatId, f)
          append([f])
        },
      })
    })()

    client
      .listArtifacts(chatId)
      .then((a) => setLoadedArtifacts(a))
      .catch(() => setLoadedArtifacts([]))

    return () => ac.abort()
  }, [client, chatId, append, reconnect])

  // Scheduled tasks for this chat's session — fetched on mount and (in the live
  // view) lightly polled, since a fire / a new schedule changes the list.
  // Filtering is server-side (scope), so the archive view is a one-shot fetch —
  // no point polling potentially-large cancelled/completed history.
  useEffect(() => {
    if (!chatId) {
      setTasks([])
      setArchivedTaskCount(0)
      return
    }
    let alive = true
    const load = () =>
      client
        .getTasks(chatId, scheduleScope)
        .then((r) => {
          if (!alive) return
          setTasks(r.tasks)
          setArchivedTaskCount(r.archivedCount)
        })
        .catch(() => {})
    load()
    const iv = scheduleScope === 'live' ? setInterval(load, 20_000) : undefined
    return () => {
      alive = false
      if (iv) clearInterval(iv)
    }
  }, [client, chatId, scheduleScope])

  // Advance the server read-cursor as we view this chat, so its unread badge +
  // bell entry clear. Debounced on the max frame seq seen; invalidates the
  // shared activity poll so the UI reflects it without waiting a full cycle.
  useEffect(() => {
    if (!chatId || frames.length === 0) return
    const maxSeq = frames.reduce((m, f) => Math.max(m, f.seq ?? 0), 0)
    if (maxSeq <= 0) return
    const t = setTimeout(() => {
      markChatRead(chatId, maxSeq)
        .then(() => qc.invalidateQueries({ queryKey: ['chat-activity'] }))
        .catch(() => {})
    }, 1500)
    return () => clearTimeout(t)
  }, [chatId, frames, qc])

  const view = useMemo(() => {
    const v = deriveView(frames)
    // Optimistically hide an inquiry we've already answered locally — the
    // confirming inquiry_answered / cleared-pending frame may lag or be dropped.
    if (v.inquiry && answeredIds.has(v.inquiry.request_id)) v.inquiry = null
    return v
  }, [frames, answeredIds])

  const artifacts = useMemo(() => {
    const byId = new Map<string, Artifact>()
    for (const a of loadedArtifacts) byId.set(a.id, a)
    for (const a of view.artifacts) byId.set(a.id, a)
    return [...byId.values()]
  }, [loadedArtifacts, view.artifacts])

  const sendMessage = useCallback(
    async (text: string) => {
      if (!chatId || !text.trim()) return
      await client.postMessage(chatId, text.trim())
      // A brand-new chat has no live session, so the stream never connected and
      // is idling in reconnect backoff. The message just warmed the session —
      // kick an immediate reconnect so its echo + the reply stream in now
      // instead of after the backoff delay.
      if (!connectedRef.current) setReconnect((n) => n + 1)
    },
    [client, chatId],
  )

  const cancelTurn = useCallback(async () => {
    // cascade so Stop forcibly terminates in-flight missions (the sub-agent
    // subtree), not just the root turn.
    if (chatId) await client.cancelTurn(chatId, { cascade: true })
  }, [client, chatId])

  const answerInquiry = useCallback(
    async (answer: InquiryAnswer) => {
      if (!chatId) return
      // Close the modal immediately, then submit. On failure, re-open and
      // rethrow so the caller can surface it (routing is by request_id).
      markAnswered(chatId, answer.request_id)
      setAnsweredIds((prev) => new Set(prev).add(answer.request_id))
      try {
        await client.answerInquiry(chatId, answer)
      } catch (e) {
        answeredByChat.get(chatId)?.delete(answer.request_id)
        setAnsweredIds((prev) => {
          const n = new Set(prev)
          n.delete(answer.request_id)
          return n
        })
        throw e
      }
    },
    [client, chatId],
  )

  const uploadArtifact = useCallback(
    async (file: File) => {
      if (!chatId) return
      await client.uploadArtifact(chatId, file)
      setLoadedArtifacts(await client.listArtifacts(chatId))
    },
    [client, chatId],
  )

  const downloadArtifact = useCallback(
    async (a: Artifact) => {
      if (chatId) await client.downloadArtifact(chatId, a.id, a.name)
    },
    [client, chatId],
  )

  const reloadTasks = useCallback(async () => {
    if (!chatId) return
    try {
      const r = await client.getTasks(chatId, scheduleScope)
      setTasks(r.tasks)
      setArchivedTaskCount(r.archivedCount)
    } catch {
      /* keep last known */
    }
  }, [client, chatId, scheduleScope])

  const cancelTask = useCallback(
    async (taskId: string) => {
      if (!chatId) return
      await client.cancelTask(chatId, taskId)
      await reloadTasks()
    },
    [client, chatId, reloadTasks],
  )

  const deleteTask = useCallback(
    async (taskId: string) => {
      if (!chatId) return
      await client.deleteTask(chatId, taskId)
      await reloadTasks()
    },
    [client, chatId, reloadTasks],
  )

  const setTaskPaused = useCallback(
    async (taskId: string, action: 'pause' | 'resume') => {
      if (!chatId) return
      await client.setTaskPaused(chatId, taskId, action)
      await reloadTasks()
    },
    [client, chatId, reloadTasks],
  )

  const running =
    view.status === 'active' || view.status === 'wait_subagents' || view.status === 'wait_approval'

  return {
    view,
    artifacts,
    tasks,
    archivedTaskCount,
    scheduleScope,
    toggleScheduleScope: () => setScheduleScope((s) => (s === 'all' ? 'live' : 'all')),
    cancelTask,
    deleteTask,
    setTaskPaused,
    connected,
    unreachable,
    running,
    sendMessage,
    cancelTurn,
    answerInquiry,
    uploadArtifact,
    downloadArtifact,
  }
}
