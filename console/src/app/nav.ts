import { navIcons } from '@/components/ui/icon'
import type { Persona } from '@/lib/session'

export interface NavItem {
  id: string
  label: string
  to: string
  icon: string
  /** hidden unless the effective persona is admin */
  adminOnly?: boolean
  /** badge source, resolved against live counts */
  badgeKey?: 'chats' | 'agents'
}

export interface NavGroup {
  label?: string
  items: NavItem[]
}

const GROUPS: NavGroup[] = [
  { items: [{ id: 'monitoring', label: 'Monitoring', to: '/monitoring', icon: navIcons.dashboard, adminOnly: true }] },
  {
    items: [
      { id: 'chat', label: 'Chat', to: '/chat', icon: navIcons.chat, badgeKey: 'chats' },
      { id: 'agents', label: 'Agents', to: '/agents', icon: navIcons.agents, badgeKey: 'agents' },
    ],
  },
  { label: 'Marketplace', items: [{ id: 'skills', label: 'Skills', to: '/skills', icon: navIcons.skills }] },
  {
    label: 'Platform',
    items: [
      { id: 'agentTypes', label: 'Agent Types', to: '/platform/agent-types', icon: navIcons.agents, adminOnly: true },
      { id: 'ds', label: 'Data Sources', to: '/platform/data-sources', icon: navIcons.ds, adminOnly: true },
      { id: 'cat', label: 'Catalogs', to: '/platform/catalogs', icon: navIcons.cat, adminOnly: true },
      { id: 'schema', label: 'Schema Explorer', to: '/platform/schema', icon: navIcons.schema, adminOnly: true },
      { id: 'roles', label: 'Roles & Permissions', to: '/platform/roles', icon: navIcons.roles, adminOnly: true },
      { id: 'keys', label: 'API Keys', to: '/platform/keys', icon: navIcons.keys, adminOnly: true },
    ],
  },
  { items: [{ id: 'me', label: 'Me / Access', to: '/me', icon: navIcons.me }] },
]

/** Nav groups visible for the given effective persona (empty groups dropped). */
export function navGroupsFor(persona: Persona): NavGroup[] {
  const admin = persona === 'admin'
  return GROUPS.map((g) => ({
    ...g,
    items: g.items.filter((it) => admin || !it.adminOnly),
  })).filter((g) => g.items.length > 0)
}

/** Landing route for a persona. */
export function defaultRoute(persona: Persona): string {
  return persona === 'admin' ? '/monitoring' : '/chat'
}

/** Screen title per top-level route id. */
export const SCREEN_TITLES: Record<string, string> = {
  monitoring: 'Monitoring',
  chat: 'Chat',
  agents: 'Agents',
  agentTypes: 'Agent Types',
  skills: 'Skills marketplace',
  ds: 'Data Sources',
  cat: 'Catalogs',
  schema: 'Schema Explorer',
  roles: 'Roles & Permissions',
  keys: 'API Keys',
  me: 'My access',
}

const ALL_ITEMS: NavItem[] = GROUPS.flatMap((g) => g.items)

/** Resolve the topbar screen title for a pathname (longest-prefix match). */
export function titleForPath(pathname: string): string {
  const match = ALL_ITEMS.filter((it) => pathname === it.to || pathname.startsWith(it.to + '/')).sort(
    (a, b) => b.to.length - a.to.length,
  )[0]
  return match ? SCREEN_TITLES[match.id] ?? match.label : 'Hub Console'
}
