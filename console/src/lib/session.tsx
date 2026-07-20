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
  /**
   * Admin UI is shown only when the user is really an admin AND currently
   * viewing the admin persona. This is what screens should gate affordances on
   * (lifecycle, admin tabs, all-vs-mine scope): an admin previewing "owner", or
   * anyone in the /app workspace (persona pinned to owner), gets the personal
   * surface. `isAdmin` remains the raw capability (e.g. for the /me page).
   */
  effectiveAdmin: boolean
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
  appMode = false,
  children,
}: {
  probe: IdentityProbe
  onSignOut: () => void
  /** /app workspace surface — persona is pinned to owner and cannot switch. */
  appMode?: boolean
  children: ReactNode
}) {
  const [persona, setPersonaState] = useState<Persona>(
    appMode ? 'owner' : probe.isAdmin ? 'admin' : 'owner',
  )

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
      effectiveAdmin: probe.isAdmin && persona === 'admin',
      persona,
      setPersona: (p) => !appMode && probe.isAdmin && setPersonaState(p),
      initials: initialsOf(identity.name),
      signOut: onSignOut,
    }),
    // identity fields are stable for a given probe
    [probe.isAdmin, persona, appMode, identity.name, identity.userId, onSignOut],
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useSession(): SessionCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useSession must be used within SessionProvider')
  return ctx
}
