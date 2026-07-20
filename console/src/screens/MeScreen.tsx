import { useQuery } from '@tanstack/react-query'
import { Page, ApiHint } from '@/components/shell/Page'
import { Avatar, Badge, Card, CardHeader, CardTitle, Dot, Spinner, EmptyState } from '@/components/ui'
import { useSession } from '@/lib/session'
import { probeIdentity } from '@/api/identity'
import { listMyAgentGrants } from '@/api/me'

interface Capability {
  name: string
  ok: boolean
  note: string
}

/** Capability rows, ✓-derived from the caller's admin status (`hub:management.admin`). */
function capabilities(isAdmin: boolean): Capability[] {
  return [
    { name: 'hub:management.admin', ok: isAdmin, note: isAdmin ? 'via role admin' : 'not granted' },
    { name: 'hugr:query', ok: true, note: 'GraphQL over /hugr' },
    { name: 'artifact:write', ok: true, note: 'chat artifacts' },
    { name: 'net:fetch', ok: isAdmin, note: isAdmin ? 'approval-gated' : 'not granted' },
    { name: 'skills:publish', ok: isAdmin, note: isAdmin ? 'set_skill_publish' : 'not granted' },
  ]
}

export function MeScreen() {
  const { identity, isAdmin, initials } = useSession()
  // Already prefetched at bootstrap — reuse the cached probe as the freshest
  // source of truth, falling back to the session identity.
  const probe = useQuery({ queryKey: ['identity-probe'], queryFn: probeIdentity })
  const grants = useQuery({ queryKey: ['my-agent-grants'], queryFn: listMyAgentGrants })

  const name = probe.data?.me.name ?? identity.name
  const email = probe.data?.me.email ?? identity.email
  const role = probe.data?.me.role ?? identity.role
  const userId = probe.data?.me.user_id ?? identity.userId
  const admin = probe.data?.isAdmin ?? isAdmin
  const caps = capabilities(admin)

  return (
    <Page className="max-w-[880px]">
      {/* Identity card */}
      <Card className="flex items-center gap-3.5 p-4">
        <Avatar initials={initials} size={44} />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="text-[15px] font-bold">{name}</span>
          <span className="truncate text-xs text-text3">
            {email} · user_id <span className="font-mono">{userId}</span>
          </span>
        </div>
        <Badge tone="accent" mono={false}>
          role: {role}
        </Badge>
      </Card>

      {/* Capabilities + agent grants */}
      <div className="grid items-start gap-3 md:grid-cols-2">
        <Card className="overflow-hidden">
          <CardHeader>
            <CardTitle>Capabilities</CardTitle>
          </CardHeader>
          {caps.map((c) => (
            <div
              key={c.name}
              className="flex items-center gap-2.5 border-b border-border px-4 py-2 text-sm last:border-b-0"
            >
              <span
                className="w-3.5 text-center text-xs font-bold"
                style={{ color: c.ok ? 'var(--green)' : 'var(--text3)' }}
              >
                {c.ok ? '✓' : '✗'}
              </span>
              <span className="min-w-0 flex-1 truncate font-mono text-xs">{c.name}</span>
              <span className="text-xs text-text3">{c.note}</span>
            </div>
          ))}
        </Card>

        <Card className="overflow-hidden">
          <CardHeader>
            <CardTitle>My agent grants</CardTitle>
          </CardHeader>
          {grants.isLoading ? (
            <div className="flex items-center justify-center py-8">
              <Spinner />
            </div>
          ) : !grants.data?.length ? (
            <div className="p-4">
              <EmptyState title="No agent grants" description="You do not have access to any agents yet." />
            </div>
          ) : (
            grants.data.map((g) => (
              <div
                key={g.agentId}
                className="flex items-center gap-2.5 border-b border-border px-4 py-2 text-sm last:border-b-0"
              >
                <Dot state={g.runtime} size={7} />
                <span className="min-w-0 flex-1 truncate font-semibold">{g.name}</span>
                <Badge tone={g.grant === 'owner' ? 'accent' : 'neutral'} mono={false}>
                  {g.grant}
                </Badge>
              </div>
            ))
          )}
          <div className="px-4 py-2.5 font-mono text-2xs text-text3">
            hub.db.user_agents · owner ⊃ member
          </div>
        </Card>
      </div>

      <ApiHint>function.core.auth.me + my_permissions · admin = hub:management.admin</ApiHint>
    </Page>
  )
}
