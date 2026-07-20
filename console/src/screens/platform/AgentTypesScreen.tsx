import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, Pencil, Plus, Trash2 } from 'lucide-react'
import { Page, PageHeader, ApiHint } from '@/components/shell/Page'
import {
  Button,
  Card,
  Drawer,
  Field,
  Input,
  Banner,
  Spinner,
  EmptyState,
  useToast,
  jsonParseError,
} from '@/components/ui'
import {
  listAgentTypes,
  createAgentType,
  updateAgentType,
  deleteAgentType,
  type AgentType,
} from '@/api/agent-types'
import { AgentConfigEditor } from '../agents/AgentConfigEditor'

const NEW_TYPE_TEMPLATE = `{
  "orchestration": { "image": "hugen:latest", "memory_bytes": 0, "nano_cpus": 0, "pids_limit": 0 },
  "models": { "mode": "remote", "model": "" },
  "skills": {}
}`

// A slug field derived from a JSON path, shown as the type summary.
function summarize(config: string): { model: string; image: string } {
  try {
    const c = JSON.parse(config) as Record<string, any>
    return {
      model: c?.models?.model || '—',
      image: c?.orchestration?.image || '—',
    }
  } catch {
    return { model: '?', image: '?' }
  }
}

type DraftMode = 'create' | 'edit'
interface Draft {
  mode: DraftMode
  /** original id when editing (id is immutable server-side); '' when creating. */
  origId: string
  id: string
  name: string
  description: string
  config: string
}

const idRe = /^[a-z0-9][a-z0-9-]*$/

export function AgentTypesScreen() {
  const qc = useQueryClient()
  const { success, error } = useToast()
  const types = useQuery({ queryKey: ['agentTypes'], queryFn: listAgentTypes })
  const [draft, setDraft] = useState<Draft | null>(null)

  const invalidate = () => qc.invalidateQueries({ queryKey: ['agentTypes'] })

  const openCreate = () =>
    setDraft({ mode: 'create', origId: '', id: '', name: '', description: '', config: NEW_TYPE_TEMPLATE })
  const openEdit = (t: AgentType) =>
    setDraft({ mode: 'edit', origId: t.id, id: t.id, name: t.name, description: t.description, config: t.config })
  const openCopy = (t: AgentType) =>
    setDraft({
      mode: 'create',
      origId: '',
      id: `${t.id}-copy`,
      name: `${t.name} (copy)`,
      description: t.description,
      config: t.config,
    })

  const save = useMutation({
    mutationFn: async (d: Draft) => {
      if (d.mode === 'create') {
        await createAgentType({ id: d.id, name: d.name, description: d.description, config: d.config })
      } else {
        await updateAgentType(d.origId, { name: d.name, description: d.description, config: d.config })
      }
    },
    onSuccess: (_r, d) => {
      invalidate()
      setDraft(null)
      success(
        d.mode === 'create'
          ? `insert_agent_types("${d.id}") → created`
          : `update_agent_types("${d.origId}") → saved`,
      )
    },
    onError: (e) => error(e instanceof Error ? e.message : 'save failed'),
  })

  const del = useMutation({
    mutationFn: (id: string) => deleteAgentType(id),
    onSuccess: (_r, id) => {
      invalidate()
      setDraft(null)
      success(`delete_agent_types("${id}") → deleted`)
    },
    onError: (e) => error(e instanceof Error ? e.message : 'delete failed'),
  })

  const configErr = draft ? jsonParseError(draft.config) : null
  const idErr =
    draft && draft.mode === 'create' && draft.id && !idRe.test(draft.id)
      ? 'lowercase letters, digits, hyphens; no leading hyphen'
      : null
  const canSave =
    !!draft && !!draft.id && !idErr && !!draft.name.trim() && !configErr && !save.isPending

  return (
    <Page>
      <PageHeader
        title="Agent Types"
        subtitle="Provisioning templates in hub.agent.db.agent_types — the config blob drives both the container spawn (orchestration) and the hugen runtime (models, skills, tools)."
        actions={
          <Button variant="primary" size="sm" onClick={openCreate}>
            <Plus className="h-4 w-4" /> New type
          </Button>
        }
      />

      {types.isLoading ? (
        <div className="flex items-center gap-2 px-1 py-6 text-sm text-text3">
          <Spinner /> Loading agent types…
        </div>
      ) : types.data && types.data.length > 0 ? (
        <div className="flex flex-col gap-2">
          {types.data.map((t) => {
            const s = summarize(t.config)
            return (
              <Card key={t.id} className="flex items-center gap-4 px-4 py-3">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-sm font-semibold">{t.name}</span>
                    <span className="truncate font-mono text-2xs text-text3">{t.id}</span>
                  </div>
                  {t.description && (
                    <p className="mt-0.5 truncate text-xs text-text2">{t.description}</p>
                  )}
                  <p className="mt-1 flex gap-3 font-mono text-2xs text-text3">
                    <span>model: {s.model}</span>
                    <span>image: {s.image}</span>
                  </p>
                </div>
                <div className="flex flex-none items-center gap-1.5">
                  <Button variant="secondary" size="sm" onClick={() => openEdit(t)}>
                    <Pencil className="h-3.5 w-3.5" /> Edit
                  </Button>
                  <Button variant="secondary" size="sm" onClick={() => openCopy(t)}>
                    <Copy className="h-3.5 w-3.5" /> Copy
                  </Button>
                  <Button
                    variant="danger-ghost"
                    size="sm"
                    onClick={() => {
                      if (confirm(`Delete agent type "${t.id}"? Agents referencing it will lose their template.`))
                        del.mutate(t.id)
                    }}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              </Card>
            )
          })}
        </div>
      ) : (
        <EmptyState title="No agent types" description="Create one to provision agents from it." />
      )}

      <ApiHint>
        hub.agent.db.agent_types · insert / update / delete_agent_types · config = orchestration ⊕
        hugen runtime
      </ApiHint>

      <Drawer
        open={!!draft}
        onOpenChange={(o) => !o && setDraft(null)}
        title={draft?.mode === 'create' ? 'New agent type' : `Edit ${draft?.origId ?? ''}`}
        subtitle="config feeds both the container spawn and the hugen runtime"
        width={640}
        footer={
          draft && (
            <>
              {draft.mode === 'edit' && (
                <Button
                  variant="danger-ghost"
                  size="sm"
                  className="mr-auto"
                  onClick={() => {
                    if (confirm(`Delete agent type "${draft.origId}"?`)) del.mutate(draft.origId)
                  }}
                >
                  <Trash2 className="h-3.5 w-3.5" /> Delete
                </Button>
              )}
              <Button variant="secondary" size="sm" onClick={() => setDraft(null)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                size="sm"
                disabled={!canSave}
                onClick={() => draft && save.mutate(draft)}
              >
                {draft.mode === 'create' ? 'insert_agent_types →' : 'Save'}
              </Button>
            </>
          )
        }
      >
        {draft && (
          <div className="flex flex-col gap-3.5">
            <div className="grid grid-cols-2 gap-3">
              <Field label="Type id" hint={draft.mode === 'edit' ? 'immutable' : 'e.g. data-analyst'}>
                <Input
                  value={draft.id}
                  disabled={draft.mode === 'edit'}
                  placeholder="data-analyst"
                  onChange={(e) => setDraft({ ...draft, id: e.target.value })}
                />
              </Field>
              <Field label="Name">
                <Input
                  value={draft.name}
                  placeholder="Data Analyst"
                  onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                />
              </Field>
            </div>
            {idErr && <Banner tone="error">Invalid id — {idErr}</Banner>}
            <Field label="Description">
              <Input
                value={draft.description}
                onChange={(e) => setDraft({ ...draft, description: e.target.value })}
              />
            </Field>
            <AgentConfigEditor
              value={draft.config}
              onChange={(config) => setDraft({ ...draft, config })}
            />
          </div>
        )}
      </Drawer>
    </Page>
  )
}
