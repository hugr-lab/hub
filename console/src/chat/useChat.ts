import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ChatClient, InquiryAnswer } from './client'
import { deriveView, type Artifact, type Frame } from './frames'

/**
 * Drives one chat conversation: backfills persisted frames, subscribes to the
 * live SSE stream, folds frames into a render view, and exposes turn actions.
 */
export function useChat(client: ChatClient, chatId: string | null) {
  const [frames, setFrames] = useState<Frame[]>([])
  const [connected, setConnected] = useState(false)
  const [loadedArtifacts, setLoadedArtifacts] = useState<Artifact[]>([])
  const seen = useRef<Set<string>>(new Set())

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
    if (!chatId) {
      setFrames([])
      seen.current = new Set()
      return
    }
    seen.current = new Set()
    setFrames([])
    setConnected(false)
    const ac = new AbortController()

    ;(async () => {
      try {
        const backfill = await client.getEvents(chatId, 0)
        append(backfill)
      } catch {
        /* stream will still deliver live frames */
      }
      await client.openStream(chatId, {
        signal: ac.signal,
        onOpen: () => setConnected(true),
        onError: () => setConnected(false),
        onFrame: (f) => append([f]),
      })
    })()

    client
      .listArtifacts(chatId)
      .then((a) => setLoadedArtifacts(a))
      .catch(() => setLoadedArtifacts([]))

    return () => ac.abort()
  }, [client, chatId, append])

  const view = useMemo(() => deriveView(frames), [frames])

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
    },
    [client, chatId],
  )

  const cancelTurn = useCallback(async () => {
    if (chatId) await client.cancelTurn(chatId, { cascade: false })
  }, [client, chatId])

  const answerInquiry = useCallback(
    async (answer: InquiryAnswer) => {
      if (chatId) await client.answerInquiry(chatId, answer)
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

  const running =
    view.status === 'active' || view.status === 'wait_subagents' || view.status === 'wait_approval'

  return {
    view,
    artifacts,
    connected,
    running,
    sendMessage,
    cancelTurn,
    answerInquiry,
    uploadArtifact,
    downloadArtifact,
  }
}
