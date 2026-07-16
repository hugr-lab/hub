import { Navigate } from 'react-router-dom'
import { useSession } from '@/lib/session'
import { defaultRoute } from '@/app/nav'

/** Gate an admin-only route by the effective persona; owners are redirected. */
export function RequireAdmin({ children }: { children: React.ReactNode }) {
  const { persona } = useSession()
  if (persona !== 'admin') return <Navigate to={defaultRoute(persona)} replace />
  return <>{children}</>
}
