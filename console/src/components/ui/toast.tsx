import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'
import { cn } from '@/lib/cn'

export type ToastTone = 'default' | 'success' | 'error' | 'info'

interface Toast {
  id: number
  title?: string
  message: ReactNode
  tone: ToastTone
}

interface ToastApi {
  toast: (message: ReactNode, opts?: { title?: string; tone?: ToastTone }) => void
  success: (message: ReactNode, title?: string) => void
  error: (message: ReactNode, title?: string) => void
}

const Ctx = createContext<ToastApi | null>(null)

const toneBorder: Record<ToastTone, string> = {
  default: 'var(--accent)',
  success: 'var(--green)',
  error: 'var(--red)',
  info: 'var(--blue)',
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])
  const seq = useRef(0)

  const remove = useCallback((id: number) => {
    setToasts((t) => t.filter((x) => x.id !== id))
  }, [])

  const push = useCallback(
    (message: ReactNode, opts?: { title?: string; tone?: ToastTone }) => {
      const id = ++seq.current
      setToasts((t) => [...t, { id, message, title: opts?.title, tone: opts?.tone ?? 'default' }])
      setTimeout(() => remove(id), 3600)
    },
    [remove],
  )

  const api: ToastApi = {
    toast: push,
    success: (m, title) => push(m, { tone: 'success', title }),
    error: (m, title) => push(m, { tone: 'error', title }),
  }

  return (
    <Ctx.Provider value={api}>
      {children}
      <div className="pointer-events-none fixed bottom-4 right-4 z-[100] flex w-[340px] max-w-[calc(100vw-2rem)] flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            onClick={() => remove(t.id)}
            className={cn(
              'pointer-events-auto cursor-pointer rounded-btn border border-border bg-surface px-3 py-2.5 shadow-lg animate-fadeUp',
            )}
            style={{ borderLeft: `3px solid ${toneBorder[t.tone]}` }}
          >
            {t.title && <div className="text-sm font-semibold">{t.title}</div>}
            <div className="text-xs text-text2">{t.message}</div>
          </div>
        ))}
      </div>
    </Ctx.Provider>
  )
}

export function useToast(): ToastApi {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useToast must be used within ToastProvider')
  return ctx
}
