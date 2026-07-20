import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { UserManager, WebStorageStateStore, type User } from 'oidc-client-ts'
import { loadRuntimeConfig, type RuntimeConfig } from './config'
import { setTokenProvider, setUnauthorizedHandler } from './auth-token'
import { isDemoMode } from './demo'

type Status = 'loading' | 'signedOut' | 'signedIn' | 'error'

interface AuthCtx {
  status: Status
  error?: string
  login: () => void
  logout: () => void
}

const Ctx = createContext<AuthCtx | null>(null)

function createManager(cfg: RuntimeConfig): UserManager {
  const base = window.location.origin + import.meta.env.BASE_URL // …/console/
  return new UserManager({
    authority: cfg.oidc_issuer,
    client_id: cfg.oidc_client_id,
    redirect_uri: base,
    post_logout_redirect_uri: base,
    response_type: 'code',
    scope: cfg.oidc_scopes,
    automaticSilentRenew: true,
    loadUserInfo: true,
    // Seed metadata from the hub so token/userinfo/jwks resolve to its
    // same-origin OIDC proxy (no provider CORS needed). Skips client-side
    // discovery entirely. Absent → oidc-client-ts discovers from the issuer.
    ...(cfg.oidc ? { metadata: cfg.oidc } : {}),
    // Tokens live in localStorage so a reload keeps the session; the bearer is
    // always read fresh from the manager per request.
    userStore: new WebStorageStateStore({ store: window.localStorage }),
  })
}

const hasAuthCallback = () => {
  const p = new URLSearchParams(window.location.search)
  return p.has('code') && p.has('state')
}

const cleanUrl = () => {
  const url = new URL(window.location.href)
  url.search = ''
  window.history.replaceState({}, '', url.toString())
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status>('loading')
  const [error, setError] = useState<string>()
  const managerRef = useRef<UserManager | null>(null)

  const login = useCallback(() => {
    // The registered redirect_uri is always …/console/ (the fixed Vite base), so
    // a cold login from the /app workspace would otherwise land on /console.
    // Carry the current path in the OIDC `state` and restore it on callback — no
    // second redirect_uri to register with the provider.
    const returnTo = window.location.pathname + window.location.search
    managerRef.current?.signinRedirect({ state: { returnTo } }).catch((e) => setError(String(e)))
  }, [])

  const logout = useCallback(() => {
    const um = managerRef.current
    if (!um) return
    um.signoutRedirect().catch(() => {
      um.removeUser()
      setStatus('signedOut')
    })
  }, [])

  useEffect(() => {
    let cancelled = false

    // Demo mode: skip OIDC entirely.
    if (isDemoMode()) {
      setTokenProvider(() => 'demo-token')
      setStatus('signedIn')
      return
    }

    ;(async () => {
      try {
        const cfg = await loadRuntimeConfig()
        if (!cfg.oidc_issuer) throw new Error('oidc_issuer missing from /console/config.json')
        const um = createManager(cfg)
        managerRef.current = um

        setTokenProvider(async () => (await um.getUser())?.access_token ?? null)
        setUnauthorizedHandler(() => um.signinRedirect().catch(() => setStatus('signedOut')))
        um.events.addUserSignedOut(() => {
          if (!cancelled) setStatus('signedOut')
        })

        // Complete a redirect callback if we came back from the identity provider.
        let user: User | null = null
        if (hasAuthCallback()) {
          user = await um.signinRedirectCallback()
          cleanUrl()
          // Restore the pre-login surface (e.g. /app/...) — the callback always
          // lands on the registered /console/ redirect_uri. Only same-origin
          // relative paths (single leading slash, not protocol-relative) are
          // honoured, so a tampered state can't drive an open redirect.
          const returnTo = (user?.state as { returnTo?: string } | undefined)?.returnTo
          const here = window.location.pathname + window.location.search
          if (returnTo && returnTo.startsWith('/') && !returnTo.startsWith('//') && returnTo !== here) {
            window.location.replace(returnTo)
            return
          }
        } else {
          user = await um.getUser()
          if (user?.expired) {
            user = await um.signinSilent().catch(() => null)
          }
        }

        if (cancelled) return
        setStatus(user && !user.expired ? 'signedIn' : 'signedOut')
      } catch (e) {
        if (!cancelled) {
          setError(String(e instanceof Error ? e.message : e))
          setStatus('error')
        }
      }
    })()

    return () => {
      cancelled = true
    }
  }, [])

  return <Ctx.Provider value={{ status, error, login, logout }}>{children}</Ctx.Provider>
}

export function useAuth(): AuthCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
