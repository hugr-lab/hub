import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Page, PageHeader, ApiHint } from '@/components/shell/Page'
import {
  DataTable,
  Button,
  Badge,
  Dot,
  Banner,
  Spinner,
  EmptyState,
  dotColor,
  useToast,
  type Column,
} from '@/components/ui'
import { useSession } from '@/lib/session'
import {
  listAgents,
  startAgent,
  stopAgent,
  disableAgent,
  deleteAgent,
  type Agent,
} from '@/api/agents'
import { AgentDrawer } from './agents/AgentDrawer'
import { CreateAgentWizard } from './agents/CreateAgentWizard'

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export function AgentsScreen() {
  // effectiveAdmin: admin UI (create/start/stop/logs/all-scope) only when a real
  // admin is on the admin persona; the /app workspace + "view as owner" get the
  // personal, mine-scoped surface.
  const { effectiveAdmin: isAdmin } = useSession()
  const qc = useQueryClient()
  const { success, error } = useToast()

  const { data: agents = [], isLoading, isError } = useQuery({
    queryKey: ['agents', isAdmin],
    queryFn: () => listAgents(isAdmin),
  })

  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [wizardOpen, setWizardOpen] = useState(false)
  const [copySource, setCopySource] = useState<Agent | null>(null)

  const invalidate = () => qc.invalidateQueries({ queryKey: ['agents'] })

  const start = useMutation({
    mutationFn: startAgent,
    onSuccess: (_d, id) => {
      invalidate()
      success(`start_agent("${id}") → converging`)
    },
    onError: (e) => error(errText(e)),
  })
  const stop = useMutation({
    mutationFn: stopAgent,
    onSuccess: (_d, id) => {
      invalidate()
      success(`stop_agent("${id}") → paused`)
    },
    onError: (e) => error(errText(e)),
  })
  const disable = useMutation({
    mutationFn: disableAgent,
    onSuccess: (_d, id) => {
      invalidate()
      success(`disable_agent("${id}")`)
    },
    onError: (e) => error(errText(e)),
  })
  const del = useMutation({
    mutationFn: deleteAgent,
    onSuccess: (_d, id) => {
      invalidate()
      setSelectedId(null)
      success(`delete_agent("${id}") — destructive`)
    },
    onError: (e) => error(errText(e)),
  })

  const lifecyclePending = start.isPending || stop.isPending || disable.isPending

  const selected = agents.find((a) => a.id === selectedId) ?? null

  const columns: Column<Agent>[] = [
    {
      key: 'dot',
      header: '',
      width: '16px',
      cell: (a) => <Dot state={a.runtime_status} />,
    },
    {
      key: 'agent',
      header: 'Agent',
      width: 'minmax(0,1.3fr)',
      cell: (a) => (
        <span className="flex min-w-0 flex-col">
          <span className="truncate font-semibold">{a.name}</span>
          <span className="truncate font-mono text-2xs text-text3">{a.id}</span>
        </span>
      ),
    },
    {
      key: 'role',
      header: 'Role',
      width: 'minmax(0,1fr)',
      cell: (a) => <span className="truncate font-mono text-xs text-text2">{a.hugr_role}</span>,
    },
    {
      key: 'owner',
      header: 'Owner',
      width: '0.9fr',
      cell: (a) => <span className="truncate text-text2">{a.owner || '—'}</span>,
    },
    {
      key: 'sessions',
      header: 'Sessions',
      width: '0.7fr',
      cell: (a) => <span className="text-text2">{a.sessions}</span>,
    },
    {
      key: 'status',
      header: 'Desired / Runtime',
      width: '0.9fr',
      cell: (a) => (
        <span className="flex min-w-0 items-center gap-1.5">
          <Badge tone="neutral">{a.desired_status}</Badge>
          <span className="truncate text-xs font-semibold" style={{ color: dotColor(a.runtime_status) }}>
            {a.runtime_status}
          </span>
        </span>
      ),
    },
  ]

  if (isAdmin) {
    columns.push({
      key: 'actions',
      header: 'Actions',
      width: '150px',
      align: 'right',
      cell: (a) => {
        const canStart = a.runtime_status !== 'running' && a.desired_status !== 'disabled'
        const canStop = a.runtime_status === 'running'
        return (
          <span className="flex justify-end gap-1.5">
            <Button
              variant="secondary"
              size="sm"
              title="Provision a new agent from this one's type"
              onClick={(e) => {
                e.stopPropagation()
                setCopySource(a)
                setWizardOpen(true)
              }}
            >
              ⧉ Copy
            </Button>
            {canStart && (
              <Button
                variant="green"
                size="sm"
                disabled={lifecyclePending}
                onClick={(e) => {
                  e.stopPropagation()
                  start.mutate(a.id)
                }}
              >
                ▶ Start
              </Button>
            )}
            {canStop && (
              <Button
                variant="amber"
                size="sm"
                disabled={lifecyclePending}
                onClick={(e) => {
                  e.stopPropagation()
                  stop.mutate(a.id)
                }}
              >
                ⏸ Stop
              </Button>
            )}
          </span>
        )
      },
    })
  }

  return (
    <Page>
      <PageHeader
        title="Agents"
        subtitle={
          isAdmin
            ? `${agents.length} agents · desired state converges to runtime`
            : 'Agents you have access to — lifecycle is managed by an administrator'
        }
        actions={
          isAdmin ? (
            <Button
              variant="primary"
              size="sm"
              onClick={() => {
                setCopySource(null)
                setWizardOpen(true)
              }}
            >
              ＋ Create agent
            </Button>
          ) : undefined
        }
      />

      {isError && <Banner tone="error">Failed to load the agent fleet.</Banner>}

      <DataTable
        columns={columns}
        rows={agents}
        getKey={(a) => a.id}
        onRowClick={(a) => setSelectedId(a.id)}
        empty={
          isLoading ? (
            <div className="flex justify-center py-6">
              <Spinner size={18} />
            </div>
          ) : (
            <EmptyState
              title="No agents"
              description={
                isAdmin
                  ? 'Provision the first agent with “＋ Create agent”.'
                  : 'You have no agent grants yet.'
              }
            />
          )
        }
      />

      <ApiHint>hub.my_agent_instances · start_agent / stop_agent re-checked per call</ApiHint>

      <AgentDrawer
        agent={selected}
        isAdmin={isAdmin}
        onClose={() => setSelectedId(null)}
        onStart={(id) => start.mutate(id)}
        onStop={(id) => stop.mutate(id)}
        onDisable={(id) => disable.mutate(id)}
        onDelete={(id) => del.mutate(id)}
        lifecyclePending={lifecyclePending}
      />

      {isAdmin && (
        <CreateAgentWizard
          key={copySource?.id ?? 'new'}
          open={wizardOpen}
          onOpenChange={(o) => {
            setWizardOpen(o)
            if (!o) setCopySource(null)
          }}
          preset={
            copySource
              ? { name: `${copySource.name}-copy`, agentTypeId: copySource.agent_type_id }
              : undefined
          }
        />
      )}
    </Page>
  )
}
