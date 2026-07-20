import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Drawer,
  Tabs,
  Badge,
  Dot,
  Button,
  JsonEditor,
  Input,
  Select,
  Banner,
  Spinner,
  EmptyState,
  useToast,
  type TabDef,
} from '@/components/ui'
import {
  listAgentAccess,
  grantAgentAccess,
  revokeAgentAccess,
  updateAgent,
  fetchAgentLogs,
  type Agent,
  type AccessRole,
} from '@/api/agents'
import { SkillsTab } from './SkillsTab'

type AgentTab = 'overview' | 'config' | 'access' | 'skills' | 'logs'

const TABS: TabDef<AgentTab>[] = [
  { value: 'overview', label: 'Overview' },
  { value: 'config', label: 'Config override' },
  { value: 'access', label: 'Access grants' },
  { value: 'skills', label: 'Skills' },
]

// Logs read the container directly (admin-only endpoint), so the tab is admin-only.
const ADMIN_TABS: TabDef<AgentTab>[] = [...TABS, { value: 'logs', label: 'Logs' }]

function subtitleOf(a: Agent): string {
  return [a.id, a.version, a.created_at && `created ${a.created_at}`].filter(Boolean).join(' · ')
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export function AgentDrawer({
  agent,
  isAdmin,
  onClose,
  onStart,
  onStop,
  onDisable,
  onDelete,
  lifecyclePending,
}: {
  agent: Agent | null
  isAdmin: boolean
  onClose: () => void
  onStart: (id: string) => void
  onStop: (id: string) => void
  onDisable: (id: string) => void
  onDelete: (id: string) => void
  lifecyclePending: boolean
}) {
  return (
    <Drawer
      open={!!agent}
      onOpenChange={(o) => {
        if (!o) onClose()
      }}
      width={520}
      title={
        agent ? (
          <span className="flex items-center gap-2">
            <Dot state={agent.runtime_status} size={9} />
            {agent.name}
          </span>
        ) : undefined
      }
      subtitle={agent ? subtitleOf(agent) : undefined}
    >
      {agent && (
        <AgentDrawerBody
          key={agent.id}
          agent={agent}
          isAdmin={isAdmin}
          onStart={onStart}
          onStop={onStop}
          onDisable={onDisable}
          onDelete={onDelete}
          lifecyclePending={lifecyclePending}
        />
      )}
    </Drawer>
  )
}

function AgentDrawerBody({
  agent,
  isAdmin,
  onStart,
  onStop,
  onDisable,
  onDelete,
  lifecyclePending,
}: {
  agent: Agent
  isAdmin: boolean
  onStart: (id: string) => void
  onStop: (id: string) => void
  onDisable: (id: string) => void
  onDelete: (id: string) => void
  lifecyclePending: boolean
}) {
  const [tab, setTab] = useState<AgentTab>('overview')

  const canStart = isAdmin && agent.runtime_status !== 'running' && agent.desired_status !== 'disabled'
  const canStop = isAdmin && agent.runtime_status === 'running'
  // Owner or admin may export/install/publish skills; members see the list only.
  const canManageSkills = isAdmin || agent.access_role === 'owner'

  return (
    <div className="flex flex-col gap-4">
      <Tabs tabs={isAdmin ? ADMIN_TABS : TABS} value={tab} onChange={setTab} />
      {tab === 'overview' && (
        <OverviewTab
          agent={agent}
          isAdmin={isAdmin}
          canStart={canStart}
          canStop={canStop}
          pending={lifecyclePending}
          onStart={onStart}
          onStop={onStop}
          onDisable={onDisable}
          onDelete={onDelete}
        />
      )}
      {tab === 'config' && <ConfigTab agent={agent} isAdmin={isAdmin} />}
      {tab === 'access' && <AccessTab agent={agent} isAdmin={isAdmin} />}
      {tab === 'skills' && <SkillsTab agent={agent} canManage={canManageSkills} isAdmin={isAdmin} />}
      {tab === 'logs' && isAdmin && <LogsTab agent={agent} />}
    </div>
  )
}

/* ── logs ─────────────────────────────────────────────────────────────── */

function LogsTab({ agent }: { agent: Agent }) {
  const [tail, setTail] = useState(200)
  const logs = useQuery({
    queryKey: ['agentLogs', agent.id, tail],
    queryFn: () => fetchAgentLogs(agent.id, tail),
  })
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <span className="text-xs text-text2">Last</span>
        <Select className="w-auto" value={String(tail)} onChange={(e) => setTail(Number(e.target.value))}>
          {[100, 200, 500, 1000, 2000].map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </Select>
        <span className="text-xs text-text2">lines · container stdout+stderr</span>
        <span className="flex-1" />
        <Button variant="secondary" size="sm" disabled={logs.isFetching} onClick={() => logs.refetch()}>
          {logs.isFetching ? <Spinner size={14} /> : 'Refresh'}
        </Button>
      </div>
      {logs.isError ? (
        <Banner tone="error">
          Could not read logs — {logs.error instanceof Error ? logs.error.message : 'error'} (agent must be
          running).
        </Banner>
      ) : (
        <pre className="max-h-[440px] overflow-auto whitespace-pre-wrap rounded-btn border border-border bg-surface2 p-2.5 font-mono text-2xs leading-relaxed text-text2">
          {logs.isLoading ? 'Loading…' : logs.data || '(empty)'}
        </pre>
      )}
    </div>
  )
}

/* ── overview ─────────────────────────────────────────────────────────── */

function Fact({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex flex-col gap-0.5 rounded-[9px] border border-border px-3 py-2">
      <span className="eyebrow">{k}</span>
      <span className="truncate font-mono text-sm font-semibold" title={v}>
        {v || '—'}
      </span>
    </div>
  )
}

function OverviewTab({
  agent,
  isAdmin,
  canStart,
  canStop,
  pending,
  onStart,
  onStop,
  onDisable,
  onDelete,
}: {
  agent: Agent
  isAdmin: boolean
  canStart: boolean
  canStop: boolean
  pending: boolean
  onStart: (id: string) => void
  onStop: (id: string) => void
  onDisable: (id: string) => void
  onDelete: (id: string) => void
}) {
  const facts = [
    { k: 'hugr role', v: agent.hugr_role },
    { k: 'owner', v: agent.owner },
    { k: 'model', v: agent.model },
    { k: 'sessions', v: String(agent.sessions) },
    { k: 'desired', v: agent.desired_status },
    { k: 'runtime', v: agent.runtime_status + (agent.connected ? ' · connected' : '') },
  ]
  return (
    <>
      <div className="grid grid-cols-2 gap-2.5">
        {facts.map((f) => (
          <Fact key={f.k} k={f.k} v={f.v} />
        ))}
      </div>
      {isAdmin ? (
        <div className="flex flex-wrap items-center gap-2">
          {canStart && (
            <Button variant="green" size="sm" disabled={pending} onClick={() => onStart(agent.id)}>
              ▶ start_agent
            </Button>
          )}
          {canStop && (
            <Button variant="amber" size="sm" disabled={pending} onClick={() => onStop(agent.id)}>
              ⏸ stop_agent
            </Button>
          )}
          <Button variant="secondary" size="sm" disabled={pending} onClick={() => onDisable(agent.id)}>
            Disable
          </Button>
          <span className="flex-1" />
          <Button variant="danger-ghost" size="sm" onClick={() => onDelete(agent.id)}>
            Delete…
          </Button>
        </div>
      ) : (
        <p className="text-xs text-text3">Lifecycle is managed by an administrator.</p>
      )}
    </>
  )
}

/* ── config override ──────────────────────────────────────────────────── */

function ConfigTab({ agent, isAdmin }: { agent: Agent; isAdmin: boolean }) {
  const qc = useQueryClient()
  const { success, error } = useToast()
  const [draft, setDraft] = useState(agent.config_override)

  const jsonError = useMemo(() => {
    if (!draft.trim()) return null
    try {
      JSON.parse(draft)
      return null
    } catch (e) {
      return errText(e)
    }
  }, [draft])

  const save = useMutation({
    mutationFn: () => updateAgent(agent.id, { config_override: draft }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['agents'] })
      success(`update_agent("${agent.id}", config_override) → converge on next start`)
    },
    onError: (e) => error(errText(e)),
  })

  const dirty = draft !== agent.config_override

  return (
    <>
      <p className="text-xs text-text2">
        Per-agent <span className="font-mono">config_override</span> merged over the fleet default
        at converge time.
      </p>
      <JsonEditor value={draft} onChange={setDraft} readOnly={!isAdmin} height={280} />
      {jsonError && <Banner tone="error">Invalid JSON — {jsonError}</Banner>}
      {isAdmin && (
        <div className="flex justify-end">
          <Button
            variant="primary"
            size="sm"
            disabled={!dirty || !!jsonError || save.isPending}
            onClick={() => save.mutate()}
          >
            update_agent(config_override)
          </Button>
        </div>
      )}
    </>
  )
}

/* ── access grants ────────────────────────────────────────────────────── */

function AccessTab({ agent, isAdmin }: { agent: Agent; isAdmin: boolean }) {
  const qc = useQueryClient()
  const { success, error } = useToast()
  const [user, setUser] = useState('')
  const [role, setRole] = useState<AccessRole>('member')

  const { data: grants = [], isLoading } = useQuery({
    queryKey: ['agentAccess', agent.id],
    queryFn: () => listAgentAccess(agent.id),
  })

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['agentAccess', agent.id] })
    qc.invalidateQueries({ queryKey: ['agents'] })
  }

  const grant = useMutation({
    mutationFn: (v: { userId: string; role: AccessRole }) =>
      grantAgentAccess(agent.id, v.userId, v.role),
    onSuccess: (_d, v) => {
      invalidate()
      setUser('')
      success(`grant_agent_access("${agent.id}", "${v.userId}", "${v.role}")`)
    },
    onError: (e) => error(errText(e)),
  })

  const revoke = useMutation({
    mutationFn: (userId: string) => revokeAgentAccess(agent.id, userId),
    onSuccess: (_d, userId) => {
      invalidate()
      success(`revoke_agent_access("${agent.id}", "${userId}")`)
    },
    onError: (e) => error(errText(e)),
  })

  const submit = () => {
    const u = user.trim()
    if (u) grant.mutate({ userId: u, role })
  }

  return (
    <>
      <div className="overflow-hidden rounded-card border border-border">
        <div className="grid grid-cols-[1fr_100px_70px] gap-2.5 border-b border-border bg-surface2 px-3.5 py-2">
          <span className="eyebrow">User</span>
          <span className="eyebrow">Grant</span>
          <span className="eyebrow" />
        </div>
        {isLoading ? (
          <div className="flex justify-center py-6">
            <Spinner />
          </div>
        ) : grants.length === 0 ? (
          <div className="p-6">
            <EmptyState title="No grants" description="No users have access to this agent yet." />
          </div>
        ) : (
          grants.map((g) => (
            <div
              key={g.user_id}
              className="grid grid-cols-[1fr_100px_70px] items-center gap-2.5 border-b border-border px-3.5 py-2.5 text-sm last:border-b-0"
            >
              <span className="min-w-0">
                <span className="block truncate font-medium">{g.user_name}</span>
                {g.user_name !== g.user_id && (
                  <span className="block truncate font-mono text-2xs text-text3">{g.user_id}</span>
                )}
              </span>
              <Badge tone={g.access_role === 'owner' ? 'accent' : 'neutral'}>{g.access_role}</Badge>
              {isAdmin ? (
                <button
                  className="justify-self-end text-xs font-semibold text-red hover:underline disabled:opacity-50"
                  disabled={revoke.isPending}
                  onClick={() => revoke.mutate(g.user_id)}
                >
                  Revoke
                </button>
              ) : (
                <span />
              )}
            </div>
          ))
        )}
      </div>

      {isAdmin && (
        <div className="flex gap-2">
          <Input
            className="flex-1"
            placeholder="user id, e.g. a.novak"
            value={user}
            onChange={(e) => setUser(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit()
            }}
          />
          <Select
            className="w-auto"
            value={role}
            onChange={(e) => setRole(e.target.value as AccessRole)}
          >
            <option value="member">member</option>
            <option value="owner">owner</option>
          </Select>
          <Button variant="primary" size="sm" disabled={!user.trim() || grant.isPending} onClick={submit}>
            Grant
          </Button>
        </div>
      )}
      <p className="font-mono text-2xs text-text3">
        grant_agent_access / revoke_agent_access · revoke bites on next call
      </p>
    </>
  )
}
