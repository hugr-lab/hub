import { createContext, useContext, type ReactNode } from 'react'

/**
 * The SPA is served under two URL prefixes from ONE build:
 * - `/console` — the full management console (admin + personal surfaces).
 * - `/app`     — the personal user workspace (chat / agents / skills / me),
 *   for users who shouldn't land in the admin console.
 *
 * Assets are shared (Vite `base` stays `/console/`, so hashed asset URLs are
 * always absolute `/console/...`); only the react-router basename and the
 * rendered surface differ. In `app` mode the persona is pinned to `owner`
 * (see SessionProvider), which hides admin nav/routes/affordances.
 */
export type AppMode = 'console' | 'app'

/** Detect the mount surface from the current URL path. */
export function detectAppMode(): AppMode {
  return typeof window !== 'undefined' && window.location.pathname.startsWith('/app') ? 'app' : 'console'
}

/** react-router basename for the mode. */
export function baseForMode(mode: AppMode): string {
  return mode === 'app' ? '/app' : '/console'
}

const Ctx = createContext<AppMode>('console')

export function AppModeProvider({ mode, children }: { mode: AppMode; children: ReactNode }) {
  return <Ctx.Provider value={mode}>{children}</Ctx.Provider>
}

export function useAppMode(): AppMode {
  return useContext(Ctx)
}
