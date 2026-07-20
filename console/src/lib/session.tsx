import { createContext, useContext, useMemo, useState, type ReactNode } from 'react'
import { initialsOf } from '@/components/ui/avatar'
import type { IdentityProbe } from '@/api/identity'

export interface Identity {
  userId: string
  name: string
  email: string
  role: string
}

export type Persona = 'admin' | 'owner'

interface SessionCtx {
  identity: Identity
  /** Real capability derived from `hub:management.admin`. */
  isAdmin: boolean
  /** Effective "view as" — admins may preview the owner surface. */
  persona: Persona
  setPersona: (p: Persona) => void
  initials: string
  signOut: () => void
}

const Ctx = createContext<SessionCtx | null>(null)

/**
 * Session provider — fed by the OIDC identity probe (`me`/`my_permissions`).
 * Admins get a "view as" persona toggle to preview the owner surface; owners
 * are pinned to `owner`.
 */
export function SessionProvider({
  probe,
  onSignOut,
  children,
}: {
  probe: IdentityProbe
  onSignOut: () => void
  children: ReactNode
}) {
  const [persona, setPersonaState] = useState<Persona>(probe.isAdmin ? 'admin' : 'owner')

  const identity: Identity = {
    userId: probe.me.user_id,
    name: probe.me.name,
    email: probe.me.email ?? '',
    role: probe.me.role,
  }

  const value = useMemo<SessionCtx>(
    () => ({
      identity,
      isAdmin: probe.isAdmin,
      persona,
      setPersona: (p) => probe.isAdmin && setPersonaState(p),
      initials: initialsOf(identity.name),
      signOut: onSignOut,
    }),
    // identity fields are stable for a given probe
    [probe.isAdmin, persona, identity.name, identity.userId, onSignOut],
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useSession(): SessionCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useSession must be used within SessionProvider')
  return ctx
}
