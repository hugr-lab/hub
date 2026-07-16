import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronRight, RotateCw } from 'lucide-react'
import {
  Badge,
  Button,
  DataTable,
  EmptyState,
  SearchField,
  Segmented,
  Select,
  Spinner,
  Textarea,
  useToast,
  type Column,
  type Tone,
} from '@/components/ui'
import { ApiHint } from '@/components/shell/Page'
import { cn } from '@/lib/cn'
import {
  getNodeDetail,
  loadModelTypes,
  loadNodeChildren,
  loadRootModules,
  saveDescription,
  saveOpName,
  searchSchema,
  type DetailField,
  type SchemaHit,
  type SchemaNode,
  type SchemaNodeKind,
  type SaveDescriptionInput,
} from '@/api/schema'

// ---------------------------------------------------------------------------
// Kind chips
// ---------------------------------------------------------------------------

const KIND_CHIP: Record<SchemaNodeKind, { label: string; tone: Tone }> = {
  query: { label: 'Q', tone: 'accent' },
  mutation: { label: 'M', tone: 'amber' },
  module: { label: 'MOD', tone: 'blue' },
  table: { label: 'OBJ', tone: 'accent' },
  view: { label: 'VIEW', tone: 'green' },
  function: { label: 'FN', tone: 'amber' },
  field: { label: 'F', tone: 'neutral' },
  relation: { label: 'REL', tone: 'blue' },
}

const SELECTABLE: ReadonlySet<SchemaNodeKind> = new Set([
  'module',
  'table',
  'view',
  'function',
  'field',
])

// ---------------------------------------------------------------------------
// Recursive tree row (per-node lazy children via its own useQuery)
// ---------------------------------------------------------------------------

function TreeRow({
  node,
  depth,
  expanded,
  onToggle,
  selectedId,
  onSelect,
}: {
  node: SchemaNode
  depth: number
  expanded: Set<string>
  onToggle: (id: string) => void
  selectedId: string | null
  onSelect: (node: SchemaNode) => void
}) {
  const qc = useQueryClient()
  const isOpen = expanded.has(node.id)
  const expandable = !!node.expandable

  const childrenQ = useQuery({
    queryKey: ['schema', 'children', node.id],
    queryFn: () => loadNodeChildren(node.id),
    enabled: isOpen && expandable,
  })

  const selected = selectedId === node.id
  const selectable = SELECTABLE.has(node.kind)
  const chip = KIND_CHIP[node.kind]
  const loading = isOpen && expandable && childrenQ.isLoading

  return (
    <div>
      <div
        onClick={selectable ? () => onSelect(node) : undefined}
        className={cn(
          'group flex items-center gap-1.5 rounded-btn py-1 pr-1.5 text-sm',
          selectable && 'cursor-pointer',
          selected ? 'bg-accent-soft' : 'hover:bg-surface2',
        )}
        style={{ paddingLeft: 6 + depth * 16 }}
      >
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation()
            if (expandable) onToggle(node.id)
          }}
          className={cn('flex-none text-text3', !expandable && 'invisible')}
          aria-label={isOpen ? 'Collapse' : 'Expand'}
        >
          <ChevronRight className={cn('h-3.5 w-3.5 transition-transform', isOpen && 'rotate-90')} />
        </button>

        {loading && <Spinner size={11} className="flex-none" />}

        <Badge tone={chip.tone} className="flex-none px-1.5 py-0 text-[9px]">
          {chip.label}
        </Badge>

        <span className="min-w-0 truncate font-mono text-xs text-text">{node.name}</span>
        {node.typeLabel && (
          <span className="min-w-0 flex-1 truncate font-mono text-2xs text-text3">{node.typeLabel}</span>
        )}
        {!node.typeLabel && <span className="flex-1" />}

        {node.hasDescription && (
          <span
            title="Has description"
            className="h-[5px] w-[5px] flex-none rounded-full bg-accent"
          />
        )}

        {expandable && (
          <button
            type="button"
            title="Reload this node"
            onClick={(e) => {
              e.stopPropagation()
              if (!isOpen) onToggle(node.id)
              qc.invalidateQueries({ queryKey: ['schema', 'children', node.id] })
            }}
            className="flex-none text-text3 opacity-0 transition-opacity hover:text-accent group-hover:opacity-100"
          >
            <RotateCw className="h-3 w-3" />
          </button>
        )}
      </div>

      {isOpen &&
        childrenQ.data?.map((child) => (
          <TreeRow
            key={child.id}
            node={child}
            depth={depth + 1}
            expanded={expanded}
            onToggle={onToggle}
            selectedId={selectedId}
            onSelect={onSelect}
          />
        ))}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Screen
// ---------------------------------------------------------------------------

export function SchemaExplorerScreen() {
  const [view, setView] = useState<'tree' | 'model'>('tree')
  const [query, setQuery] = useState('')
  const [kind, setKind] = useState<'all' | 'table' | 'view' | 'function'>('all')
  const [expanded, setExpanded] = useState<Set<string>>(new Set(['query']))
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const searching = view === 'tree' && (query.trim().length > 0 || kind !== 'all')

  const rootQ = useQuery({
    queryKey: ['schema', 'root', view],
    queryFn: () => (view === 'tree' ? loadRootModules() : loadModelTypes()),
  })

  const searchQ = useQuery({
    queryKey: ['schema', 'search', query, kind],
    queryFn: () => searchSchema(query, kind),
    enabled: searching,
  })

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const selectNode = (node: SchemaNode) => setSelectedId(node.id)
  const selectId = (id: string) => setSelectedId(id)

  return (
    <div className="flex min-h-0 flex-1">
      {/* Left column — tree / model */}
      <div className="flex w-[46%] min-w-0 flex-col overflow-y-auto border-r border-border bg-surface px-2.5 py-3">
        <div className="flex items-center gap-2 px-2 pb-2">
          <span className="eyebrow flex-1">Unified GraphQL schema</span>
          <Segmented
            size="sm"
            value={view}
            onChange={(v) => setView(v)}
            options={[
              { value: 'tree', label: 'Tree' },
              { value: 'model', label: 'Model' },
            ]}
          />
        </div>

        {view === 'tree' && (
          <div className="flex items-center gap-1.5 px-1 pb-2.5">
            <SearchField
              className="flex-1"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search tables, views, functions…"
            />
            <Select
              className="w-auto"
              value={kind}
              onChange={(e) => setKind(e.target.value as typeof kind)}
            >
              <option value="all">All kinds</option>
              <option value="table">Tables</option>
              <option value="view">Views</option>
              <option value="function">Functions</option>
            </Select>
          </div>
        )}

        {rootQ.isLoading ? (
          <div className="flex items-center gap-2 px-2 py-6 text-sm text-text3">
            <Spinner /> Loading schema…
          </div>
        ) : searching ? (
          <SearchResults
            query={searchQ}
            selectedId={selectedId}
            onSelect={selectId}
          />
        ) : (
          <div className="flex flex-col">
            {(rootQ.data ?? []).map((node) => (
              <TreeRow
                key={node.id}
                node={node}
                depth={0}
                expanded={expanded}
                onToggle={toggle}
                selectedId={selectedId}
                onSelect={selectNode}
              />
            ))}
          </div>
        )}

        <div className="mt-auto px-2 pt-3">
          <ApiHint>_schema_update_*_desc · core.meta</ApiHint>
        </div>
      </div>

      {/* Right column — node detail */}
      <div className="min-w-[340px] flex-1 overflow-y-auto px-[18px] py-4">
        <DetailPanel selectedId={selectedId} />
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Search results list
// ---------------------------------------------------------------------------

function SearchResults({
  query,
  selectedId,
  onSelect,
}: {
  query: { isLoading: boolean; data?: SchemaHit[] }
  selectedId: string | null
  onSelect: (id: string) => void
}) {
  if (query.isLoading) {
    return (
      <div className="flex items-center gap-2 px-2 py-6 text-sm text-text3">
        <Spinner /> Searching…
      </div>
    )
  }
  const hits = query.data ?? []
  if (hits.length === 0) {
    return (
      <div className="px-3 py-5 text-center text-sm text-text3">
        Nothing matches — try a shorter query.
      </div>
    )
  }
  return (
    <div className="flex flex-col">
      {hits.map((h) => {
        const chip = KIND_CHIP[h.kind]
        const selected = selectedId === h.id
        return (
          <div
            key={h.id}
            onClick={() => onSelect(h.id)}
            className={cn(
              'flex cursor-pointer items-center gap-2 rounded-btn px-2.5 py-1.5 text-sm',
              selected ? 'bg-accent-soft' : 'hover:bg-surface2',
            )}
          >
            <Badge tone={chip.tone} className="px-1.5 py-0 text-[9px]">
              {chip.label}
            </Badge>
            <span className="font-mono text-xs font-semibold text-text">{h.name}</span>
            <span className="text-2xs text-text3">{h.kind}</span>
            <span className="font-mono text-2xs text-text3">{h.module}</span>
            <span className="flex-1" />
            <span className="text-2xs text-text3">{h.fieldCount} fields</span>
          </div>
        )
      })}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Detail panel — badges + description editor + fields + relations
// ---------------------------------------------------------------------------

function DetailPanel({ selectedId }: { selectedId: string | null }) {
  const qc = useQueryClient()
  const { success, error } = useToast()

  const detailQ = useQuery({
    queryKey: ['schema', 'detail', selectedId],
    queryFn: () => getNodeDetail(selectedId as string),
    enabled: !!selectedId,
  })
  const detail = detailQ.data

  const [descDraft, setDescDraft] = useState('')
  const [editingField, setEditingField] = useState<string | null>(null)
  const [fieldDraft, setFieldDraft] = useState('')

  // Reset drafts only when the *selected node* changes (a refetch after save
  // keeps the same id, so the textarea is not clobbered).
  const detailId = detail?.id
  useEffect(() => {
    setDescDraft(detail?.description ?? '')
    setEditingField(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detailId])

  const saveMut = useMutation({
    mutationFn: saveDescription,
    onSuccess: (_res, vars) => {
      success(saveOpName(vars))
      if (vars.kind === 'field') setEditingField(null)
      qc.invalidateQueries({ queryKey: ['schema', 'detail', selectedId] })
      qc.invalidateQueries({ queryKey: ['schema', 'children'] })
    },
    onError: (e) => error(e instanceof Error ? e.message : 'Save failed'),
  })

  const fieldCols: Column<DetailField>[] = useMemo(
    () => [
      {
        key: 'ord',
        header: '#',
        width: '30px',
        cell: (f) => <span className="font-mono text-2xs text-text3">{f.ordinal}</span>,
      },
      {
        key: 'name',
        header: 'Name',
        width: 'minmax(0,1fr)',
        cell: (f) => (
          <span
            className={cn(
              'break-all font-mono text-xs font-semibold',
              editingField === f.name && 'text-accent',
            )}
          >
            {f.name}
          </span>
        ),
      },
      {
        key: 'type',
        header: 'Type',
        width: 'minmax(0,1fr)',
        cell: (f) => <span className="font-mono text-xs text-accent">{f.type}</span>,
      },
      {
        key: 'desc',
        header: 'Description',
        width: 'minmax(0,1.4fr)',
        cell: (f) =>
          f.description ? (
            <span className="text-xs text-text2">{f.description}</span>
          ) : (
            <span className="text-xs text-text3">—</span>
          ),
      },
    ],
    [editingField],
  )

  if (!selectedId) {
    return (
      <EmptyState
        className="mt-2"
        title="No node selected"
        description="Select a source, module, type or field to view and edit its description."
      />
    )
  }
  if (detailQ.isLoading || !detail) {
    return (
      <div className="flex items-center gap-2 py-6 text-sm text-text3">
        <Spinner /> Loading…
      </div>
    )
  }

  const chip = KIND_CHIP[detail.kind]
  const canSave = detail.saveKind !== null

  const startFieldEdit = (f: DetailField) => {
    setEditingField(f.name)
    setFieldDraft(f.description)
  }

  const saveNodeDesc = () => {
    if (!detail.saveKind) return
    const input: SaveDescriptionInput = {
      kind: detail.saveKind,
      target:
        detail.saveKind === 'field'
          ? { name: detail.name, typeName: detail.typeName }
          : { name: detail.name },
      description: descDraft,
    }
    saveMut.mutate(input)
  }

  const saveFieldDesc = (f: DetailField) => {
    const input: SaveDescriptionInput = {
      kind: 'field',
      target: { name: f.name, typeName: detail.name },
      description: fieldDraft,
    }
    saveMut.mutate(input)
  }

  return (
    <div className="flex flex-col gap-3">
      {/* header */}
      <div className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2">
          <Badge tone={chip.tone} className="px-1.5 py-0 text-[9px]">
            {chip.label}
          </Badge>
          <span className="break-all font-mono text-base font-bold">{detail.name}</span>
        </div>
        {detail.badges.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {detail.badges.map((b, i) => (
              <Badge key={i} tone={b.tone}>
                {b.text}
              </Badge>
            ))}
          </div>
        )}
        {detail.meta && <span className="text-2xs text-text3">{detail.meta}</span>}
      </div>

      {/* description editor */}
      <div className="flex flex-col gap-1.5">
        <span className="text-xs font-semibold text-text2">Description</span>
        {canSave ? (
          <>
            <Textarea
              rows={6}
              value={descDraft}
              onChange={(e) => setDescDraft(e.target.value)}
              placeholder="Describe this for humans and agents — descriptions feed LLM schema summaries."
            />
            <div className="flex justify-end">
              <Button
                variant="primary"
                size="sm"
                disabled={saveMut.isPending}
                onClick={saveNodeDesc}
              >
                {saveMut.isPending ? 'Saving…' : 'Save description'}
              </Button>
            </div>
          </>
        ) : (
          <span className="text-xs text-text3">
            Operation roots have no editable description — pick a module, type or field.
          </span>
        )}
      </div>

      {/* fields */}
      {detail.fields.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <span className="text-sm font-bold">Fields ({detail.fields.length})</span>
          <DataTable
            columns={fieldCols}
            rows={detail.fields}
            getKey={(f) => f.name}
            onRowClick={(f) => startFieldEdit(f)}
          />
          <span className="text-2xs text-text3">
            Click a field to edit its description — sourced from metadata queries (core.meta).
          </span>

          {editingField &&
            (() => {
              const f = detail.fields.find((x) => x.name === editingField)
              if (!f) return null
              return (
                <div className="flex flex-col gap-2 rounded-card border border-border2 bg-surface2 p-3">
                  <div className="flex items-center gap-2">
                    <span className="text-2xs font-semibold uppercase tracking-[0.05em] text-text3">
                      Editing field
                    </span>
                    <span className="font-mono text-xs font-semibold">{f.name}</span>
                    <span className="font-mono text-2xs text-accent">{f.type}</span>
                  </div>
                  <Textarea
                    rows={3}
                    value={fieldDraft}
                    onChange={(e) => setFieldDraft(e.target.value)}
                    placeholder="Field description — feeds the field-level LLM summary."
                  />
                  <div className="flex justify-end gap-2">
                    <Button variant="ghost" size="sm" onClick={() => setEditingField(null)}>
                      Cancel
                    </Button>
                    <Button
                      variant="primary"
                      size="sm"
                      disabled={saveMut.isPending}
                      onClick={() => saveFieldDesc(f)}
                    >
                      {saveMut.isPending ? 'Saving…' : 'Save field'}
                    </Button>
                  </div>
                  <span className="font-mono text-2xs text-text3">
                    _schema_update_field_desc(type_name:&quot;{detail.name}&quot;, name:&quot;{f.name}&quot;)
                  </span>
                </div>
              )
            })()}
        </div>
      )}

      {/* relations */}
      {detail.relations.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <span className="text-sm font-bold">Relations</span>
          <div className="overflow-hidden rounded-card border border-border">
            {detail.relations.map((r, i) => (
              <div
                key={i}
                className="flex items-center gap-2 border-b border-border px-3 py-1.5 text-xs last:border-b-0"
              >
                <Badge tone={r.direction === 'in' ? 'blue' : 'accent'}>{r.direction}</Badge>
                <span className="font-mono text-text2">
                  {r.direction === 'in' ? `${r.target} → ${r.name}` : `${r.name} → ${r.target}`}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
