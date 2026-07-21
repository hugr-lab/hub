import { useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Badge, Banner, Button, EmptyState, Spinner, useToast } from '@/components/ui'
import {
  listAgentSkills,
  exportAgentSkill,
  installAgentSkill,
  publishAgentSkillToMarketplace,
  type AgentSkill,
} from '@/api/agent-skills'
import type { Agent } from '@/api/agents'

// Origins in display order; the rest fall through under "other".
const ORIGIN_ORDER = ['hub', 'local', 'dynamic', 'system', 'inline'] as const
const ORIGIN_LABEL: Record<string, string> = {
  hub: 'Hub',
  local: 'Local',
  dynamic: 'Local (indexed)',
  system: 'System',
  inline: 'Inline',
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

/**
 * The agent's installed skills, grouped by origin. Listing is member+; export,
 * install-from-bundle, and publish are shown only to owners/admins (the hub
 * enforces the same gate — the UI just hides what would 403).
 */
export function SkillsTab({
  agent,
  canManage,
  isAdmin,
}: {
  agent: Agent
  canManage: boolean
  isAdmin: boolean
}) {
  const qc = useQueryClient()
  const { success, error: toastError } = useToast()
  const running = agent.runtime_status === 'running'
  const fileRef = useRef<HTMLInputElement>(null)
  const [overwrite, setOverwrite] = useState(false)

  const {
    data: skills = [],
    isLoading,
    isError,
    error,
  } = useQuery({
    queryKey: ['agent-skills', agent.id],
    queryFn: () => listAgentSkills(agent.id),
    enabled: running,
  })

  const install = useMutation({
    mutationFn: (bundle: Blob) => installAgentSkill(agent.id, bundle, overwrite),
    onSuccess: () => {
      success('Skill installed')
      qc.invalidateQueries({ queryKey: ['agent-skills', agent.id] })
    },
    onError: (e) => toastError(errText(e)),
  })

  const publish = useMutation({
    mutationFn: (name: string) => publishAgentSkillToMarketplace(agent.id, name),
    onSuccess: () => success('Published to marketplace'),
    onError: (e) => toastError(errText(e)),
  })

  const doExport = async (name: string) => {
    try {
      const blob = await exportAgentSkill(agent.id, name)
      downloadBlob(blob, `${name}.tar.gz`)
    } catch (e) {
      toastError(errText(e))
    }
  }

  if (!running) {
    return <Banner tone="info">The agent isn't running — start it to view its installed skills.</Banner>
  }
  if (isLoading) {
    return (
      <div className="flex items-center gap-2 py-6 text-sm text-text2">
        <Spinner /> Loading skills…
      </div>
    )
  }
  if (isError) {
    return <Banner tone="error">Couldn't load skills: {errText(error)}</Banner>
  }

  const groups = groupByOrigin(skills)

  return (
    <div className="flex flex-col gap-4">
      {canManage && (
        <div className="flex flex-wrap items-center gap-2 rounded-panel border border-border bg-surface2 p-3">
          <input
            ref={fileRef}
            type="file"
            accept=".tar.gz,.tgz,.gz,application/gzip"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0]
              if (f) install.mutate(f)
              e.target.value = ''
            }}
          />
          <Button size="sm" onClick={() => fileRef.current?.click()} disabled={install.isPending}>
            {install.isPending ? 'Installing…' : '↥ Install bundle'}
          </Button>
          <label className="flex items-center gap-1.5 text-2xs text-text2">
            <input type="checkbox" checked={overwrite} onChange={(e) => setOverwrite(e.target.checked)} />
            Overwrite if a local skill with the same name exists
          </label>
        </div>
      )}

      {skills.length === 0 ? (
        <EmptyState title="No skills installed" />
      ) : (
        groups.map(([origin, list]) => (
          <div key={origin} className="flex flex-col gap-1.5">
            <div className="eyebrow px-0.5">{ORIGIN_LABEL[origin] ?? origin}</div>
            {list.map((sk) => (
              <SkillRow
                key={`${origin}:${sk.name}`}
                sk={sk}
                canManage={canManage}
                isAdmin={isAdmin}
                onExport={() => doExport(sk.name)}
                onPublish={() => publish.mutate(sk.name)}
                publishing={publish.isPending && publish.variables === sk.name}
              />
            ))}
          </div>
        ))
      )}
    </div>
  )
}

function SkillRow({
  sk,
  canManage,
  isAdmin,
  onExport,
  onPublish,
  publishing,
}: {
  sk: AgentSkill
  canManage: boolean
  isAdmin: boolean
  onExport: () => void
  onPublish: () => void
  publishing: boolean
}) {
  return (
    <div className="flex items-start gap-2 rounded-btn border border-border bg-surface px-3 py-2">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[13px] font-semibold text-text">{sk.name}</span>
          {sk.task_eligible && <Badge tone="accent">task</Badge>}
          {sk.tiers?.map((t) => (
            <Badge key={t}>{t}</Badge>
          ))}
        </div>
        {sk.description && <span className="text-2xs text-text2 line-clamp-2">{sk.description}</span>}
      </div>
      {/* System skills are agent-core (embedded) — not user-exported/published. */}
      {canManage && sk.origin !== 'system' && (
        <div className="flex flex-none items-center gap-1">
          {sk.exportable && (
            <Button size="sm" variant="ghost" onClick={onExport} title="Download bundle (.tar.gz)">
              ↧ Export
            </Button>
          )}
          {isAdmin && sk.exportable && (
            <Button size="sm" variant="ghost" onClick={onPublish} disabled={publishing} title="Publish to the marketplace">
              {publishing ? '…' : '⇪ Publish'}
            </Button>
          )}
        </div>
      )}
    </div>
  )
}

function groupByOrigin(skills: AgentSkill[]): [string, AgentSkill[]][] {
  const by = new Map<string, AgentSkill[]>()
  for (const sk of skills) {
    const arr = by.get(sk.origin) ?? []
    arr.push(sk)
    by.set(sk.origin, arr)
  }
  const order = (o: string) => {
    const i = ORIGIN_ORDER.indexOf(o as (typeof ORIGIN_ORDER)[number])
    return i === -1 ? ORIGIN_ORDER.length : i
  }
  return [...by.entries()].sort((a, b) => order(a[0]) - order(b[0]))
}
