import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Badge, Banner, Button, EmptyState, Input, Select, Spinner, useToast } from '@/components/ui'
import {
  listToolProviders,
  upsertToolProvider,
  deleteToolProvider,
  providerErrText,
  type ToolProvider,
  type ToolProviderInput,
} from '@/api/agent-tool-providers'
import type { Agent } from '@/api/agents'

const EMPTY_FORM: ToolProviderInput = { name: '', transport: 'http', endpoint: '', auth: '' }

/**
 * Remote MCP tool providers on the agent. Listing is member+; add/remove are
 * owner/admin (the hub enforces the same gate). Providers are per_agent, so a
 * change is applied to the running agent and becomes live in every session.
 */
export function ToolsTab({ agent, canManage }: { agent: Agent; canManage: boolean }) {
  const qc = useQueryClient()
  const { toast, success, error: toastError } = useToast()
  const running = agent.runtime_status === 'running'
  const [adding, setAdding] = useState(false)
  const [form, setForm] = useState<ToolProviderInput>(EMPTY_FORM)

  const key = ['agent-tool-providers', agent.id]
  const { data: providers = [], isLoading, isError, error } = useQuery({
    queryKey: key,
    queryFn: () => listToolProviders(agent.id),
    enabled: running,
  })

  const upsert = useMutation({
    mutationFn: (input: ToolProviderInput) => upsertToolProvider(agent.id, input),
    onSuccess: (res) => {
      if (res?.applied === false) {
        toast('Saved — but the agent is unreachable, so it will apply on the next reload.', { tone: 'info' })
      } else {
        success('MCP server saved')
      }
      setAdding(false)
      setForm(EMPTY_FORM)
      qc.invalidateQueries({ queryKey: key })
    },
    onError: (e) => toastError(providerErrText(e)),
  })

  const remove = useMutation({
    mutationFn: (name: string) => deleteToolProvider(agent.id, name),
    onSuccess: (res) => {
      if (res?.applied === false) {
        toast('Removed from config — but still live on the agent until it reloads.', { tone: 'info' })
      } else {
        success('MCP server removed')
      }
      qc.invalidateQueries({ queryKey: key })
    },
    onError: (e) => toastError(providerErrText(e)),
  })

  if (!running) {
    return <Banner tone="info">The agent isn't running — start it to view or edit its MCP tool providers.</Banner>
  }
  if (isLoading) {
    return (
      <div className="flex items-center gap-2 py-6 text-sm text-text2">
        <Spinner /> Loading tool providers…
      </div>
    )
  }
  if (isError) {
    return <Banner tone="error">Couldn't load tool providers: {providerErrText(error)}</Banner>
  }

  const submit = () => {
    if (!form.name.trim() || !form.endpoint.trim()) return
    upsert.mutate({ ...form, name: form.name.trim(), endpoint: form.endpoint.trim(), auth: form.auth?.trim() || undefined })
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="text-2xs text-text3">
        Remote MCP servers registered on the agent (per-agent — changes apply to every session).
      </div>

      {providers.length === 0 ? (
        <EmptyState title="No remote MCP providers" />
      ) : (
        <div className="flex flex-col gap-1.5">
          {providers.map((p) => (
            <ProviderRow key={p.name} p={p} canManage={canManage} onDelete={() => remove.mutate(p.name)} deleting={remove.isPending && remove.variables === p.name} />
          ))}
        </div>
      )}

      {canManage &&
        (adding ? (
          <div className="flex flex-col gap-2 rounded-panel border border-border bg-surface2 p-3">
            <div className="eyebrow">Add MCP server</div>
            <Input placeholder="name (e.g. weather-mcp)" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
            <Input placeholder="endpoint (https://…/mcp)" value={form.endpoint} onChange={(e) => setForm({ ...form, endpoint: e.target.value })} />
            <div className="flex gap-2">
              <Select value={form.transport} onChange={(e) => setForm({ ...form, transport: e.target.value })} className="flex-1">
                <option value="http">http (streamable)</option>
                <option value="sse">sse</option>
              </Select>
              <Input placeholder="auth source (optional)" value={form.auth} onChange={(e) => setForm({ ...form, auth: e.target.value })} className="flex-1" />
            </div>
            <div className="flex justify-end gap-2">
              <Button size="sm" variant="ghost" onClick={() => { setAdding(false); setForm(EMPTY_FORM) }}>
                Cancel
              </Button>
              <Button size="sm" onClick={submit} disabled={upsert.isPending || !form.name.trim() || !form.endpoint.trim()}>
                {upsert.isPending ? 'Saving…' : 'Save & apply'}
              </Button>
            </div>
          </div>
        ) : (
          <Button size="sm" variant="ghost" className="self-start" onClick={() => setAdding(true)}>
            ＋ Add MCP server
          </Button>
        ))}
    </div>
  )
}

function ProviderRow({
  p,
  canManage,
  onDelete,
  deleting,
}: {
  p: ToolProvider
  canManage: boolean
  onDelete: () => void
  deleting: boolean
}) {
  return (
    <div className="flex items-start gap-2 rounded-btn border border-border bg-surface px-3 py-2">
      <span
        className="mt-1 h-[7px] w-[7px] flex-none rounded-full"
        style={{ background: p.live ? 'var(--green)' : 'var(--text3)' }}
        title={p.live ? 'live on the agent' : 'not yet applied'}
      />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[13px] font-semibold text-text">{p.name}</span>
          <Badge>{p.transport}</Badge>
          {p.auth && <Badge tone="blue">auth: {p.auth}</Badge>}
        </div>
        {p.endpoint && <span className="truncate text-2xs text-text3">{p.endpoint}</span>}
      </div>
      {canManage && (
        <Button size="sm" variant="danger-ghost" onClick={onDelete} disabled={deleting} title="Remove this MCP server">
          {deleting ? '…' : 'Remove'}
        </Button>
      )}
    </div>
  )
}
