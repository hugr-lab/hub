import { useQuery } from '@tanstack/react-query'
import { useAuth } from '@/lib/auth'
import { SessionProvider } from '@/lib/session'
import { probeIdentity } from '@/api/identity'
import { LoginScreen } from '@/screens/LoginScreen'
import { Spinner } from '@/components/ui'

function Splash({ message }: { message?: string }) {
  return (
    <div className="flex h-screen w-full flex-col items-center justify-center gap-3 bg-bg">
      <img src="/console/logo.svg" alt="hugr" className="h-10 w-10 opacity-80" />
      <div className="flex items-center gap-2 text-sm text-text2">
        <Spinner /> {message ?? 'Loading…'}
      </div>
    </div>
  )
}

function ErrorCard({ title, detail }: { title: string; detail?: string }) {
  return (
    <div className="flex h-screen w-full items-center justify-center bg-bg">
      <div className="w-[380px] max-w-[calc(100vw-2rem)] rounded-modal border border-red/40 bg-surface p-6 text-center shadow-card">
        <div className="text-sm font-semibold text-red">{title}</div>
        {detail && <div className="mt-2 break-words text-xs text-text2">{detail}</div>}
      </div>
    </div>
  )
}

/**
 * Gate: waits for OIDC, shows the login screen when signed out, then runs the
 * identity probe and mounts the session for the app.
 */
export function AuthGate({ children }: { children: React.ReactNode }) {
  const { status, error, login, logout } = useAuth()

  const probe = useQuery({
    queryKey: ['identity-probe'],
    queryFn: probeIdentity,
    enabled: status === 'signedIn',
    staleTime: Infinity,
    retry: false,
  })

  if (status === 'loading') return <Splash message="Connecting…" />
  if (status === 'error') return <ErrorCard title="Sign-in unavailable" detail={error} />
  if (status === 'signedOut') return <LoginScreen onLogin={login} error={error} />

  // signedIn — resolve identity
  if (probe.isLoading) return <Splash message="Loading your access…" />
  if (probe.isError || !probe.data)
    return <ErrorCard title="Could not load your identity" detail={String(probe.error ?? '')} />

  return (
    <SessionProvider probe={probe.data} onSignOut={logout}>
      {children}
    </SessionProvider>
  )
}
