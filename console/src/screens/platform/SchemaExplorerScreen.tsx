import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronRight, RotateCw } from 'lucide-react'
import {
  Badge,
  Button,
  DataTable,
  EmptyState,
  Segmented,
  Spinner,
  Textarea,
  useToast,
  type Column,
  type Tone,
} from '@/components/ui'
import { ApiHint } from '@/components/shell/Page'
import { cn } from '@/lib/cn'
import {
  loadChildren,
  loadDetail,
  loadRoots,
  saveDescription,
  saveOpName,
  type DetailField,
  type NodeKind,
  type SchemaNode,
  type SchemaTree,
  type SaveDescriptionInput,
} from '@/api/schema'

// ── kind chips ──────────────────────────────────────────────────────────────

const KIND_CHIP: Record<NodeKind, { label: string; tone: Tone } | null> = {
  root: { label: 'ROOT', tone: 'neutral' },
  module: { label: 'MOD', tone: 'blue' },
  object: { label: 'OBJ', tone: 'accent' },
  view: { label: 'VIEW', tone: 'green' },
  function: { label: 'FN', tone: 'amber' },
  // Plain fields carry no chip — the label + type + hugr_type badge say enough
  // (an "F" on every row is just noise).
  field: null,
  arg: { label: 'ARG', tone: 'neutral' },
  inputField: { label: 'IN', tone: 'neutral' },
  enumValue: null,
  relation: { label: 'REL', tone: 'blue' },
  group: null,
}

// ── recursive tree row (per-node lazy children) ─────────────────────────────

function TreeRow({
  node,
  tree,
  depth,
  expanded,
  onToggle,
  selectedId,
  onSelect,
}: {
  node: SchemaNode
  tree: SchemaTree
  depth: number
  expanded: Set<string>
  onToggle: (id: string) => void
  selectedId: string | null
  onSelect: (node: SchemaNode) => void
}) {
  const qc = useQueryClient()
  const isOpen = expanded.has(node.id)
  const expandable = node.expandable

  const childrenQ = useQuery({
    queryKey: ['schema', 'children', tree, node.id],
    queryFn: () => loadChildren(node),
    enabled: isOpen && expandable,
  })

  const selected = selectedId === node.id
  const chip = KIND_CHIP[node.kind]
  const loading = isOpen && expandable && childrenQ.isLoading

  return (
    <div>
      <div
        onClick={node.selectable ? () => onSelect(node) : undefined}
        className={cn(
          'group flex items-center gap-1.5 rounded-btn py-1 pr-1.5 text-sm',
          node.selectable && 'cursor-pointer',
          selected ? 'bg-accent-soft' : 'hover:bg-surface2',
        )}
        style={{ paddingLeft: 6 + depth * 15 }}
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

        {chip && (
          <Badge tone={chip.tone} className="flex-none px-1.5 py-0 text-[9px]">
            {chip.label}
          </Badge>
        )}

        <span className="whitespace-nowrap font-mono text-xs text-text">{node.label}</span>
        {node.badges?.map((b, i) => (
          <Badge key={i} tone={b.tone as Tone} className="flex-none px-1 py-0 text-[9px]">
            {b.text}
          </Badge>
        ))}
        {node.typeLabel && (
          <span className="whitespace-nowrap font-mono text-2xs text-text3">{node.typeLabel}</span>
        )}
        <span className="flex-1" />

        {node.hasDescription && (
          <span title="Has description" className="h-[5px] w-[5px] flex-none rounded-full bg-accent" />
        )}

        {expandable && (
          <button
            type="button"
            title="Reload this node"
            onClick={(e) => {
              e.stopPropagation()
              if (!isOpen) onToggle(node.id)
              qc.invalidateQueries({ queryKey: ['schema', 'children', tree, node.id] })
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
            tree={tree}
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

// ── screen ──────────────────────────────────────────────────────────────────

export function SchemaExplorerScreen() {
  const qc = useQueryClient()
  const [tree, setTree] = useState<SchemaTree>('logical')
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [selected, setSelected] = useState<SchemaNode | null>(null)

  const rootQ = useQuery({
    queryKey: ['schema', 'roots', tree],
    queryFn: () => loadRoots(tree),
  })

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const switchTree = (t: SchemaTree) => {
    setTree(t)
    setExpanded(new Set())
    setSelected(null)
  }

  const refreshAll = () => qc.invalidateQueries({ queryKey: ['schema'] })

  return (
    <div className="flex min-h-0 flex-1">
      {/* left — tree */}
      <div className="flex w-[46%] min-w-0 flex-col overflow-hidden border-r border-border bg-surface">
        <div className="flex items-center gap-2 px-3 py-2.5">
          <Segmented
            size="sm"
            value={tree}
            onChange={switchTree}
            options={[
              { value: 'logical', label: 'Logical model' },
              { value: 'graphql', label: 'GraphQL' },
            ]}
          />
          <span className="flex-1" />
          <button
            type="button"
            title="Refresh the whole tree"
            onClick={refreshAll}
            className="flex items-center gap-1 rounded-btn px-2 py-1 text-2xs text-text3 hover:bg-surface2 hover:text-accent"
          >
            <RotateCw className={cn('h-3.5 w-3.5', rootQ.isFetching && 'animate-spin')} />
            Refresh
          </button>
        </div>

        <div className="min-h-0 flex-1 overflow-auto px-2 pb-3">
          {rootQ.isLoading ? (
            <div className="flex items-center gap-2 px-2 py-6 text-sm text-text3">
              <Spinner /> Loading schema…
            </div>
          ) : rootQ.isError ? (
            <EmptyState
              className="mt-2"
              title="Couldn't load schema"
              description={rootQ.error instanceof Error ? rootQ.error.message : undefined}
            />
          ) : (
            // w-max so deeply-indented rows grow past the panel and the
            // container scrolls horizontally; min-w-full keeps highlights full-
            // width when content is narrow.
            <div className="flex w-max min-w-full flex-col">
              {(rootQ.data ?? []).map((node) => (
                <TreeRow
                  key={node.id}
                  node={node}
                  tree={tree}
                  depth={0}
                  expanded={expanded}
                  onToggle={toggle}
                  selectedId={selected?.id ?? null}
                  onSelect={setSelected}
                />
              ))}
              {(rootQ.data ?? []).length === 0 && (
                <p className="px-2 py-6 text-sm text-text3">Nothing visible for your role.</p>
              )}
            </div>
          )}
        </div>

        <div className="border-t border-border px-3 py-2">
          <ApiHint>{tree === 'logical' ? '_catalog · _module · _dataObject' : '__schema · __type'}</ApiHint>
        </div>
      </div>

      {/* right — detail */}
      <div className="min-w-[340px] flex-1 overflow-y-auto px-[18px] py-4">
        <DetailPanel node={selected} tree={tree} />
      </div>
    </div>
  )
}

// ── detail panel ─────────────────────────────────────────────────────────────

function DetailPanel({ node, tree }: { node: SchemaNode | null; tree: SchemaTree }) {
  const qc = useQueryClient()
  const { success, error } = useToast()

  const detailQ = useQuery({
    queryKey: ['schema', 'detail', tree, node?.id],
    queryFn: () => loadDetail(node as SchemaNode),
    enabled: !!node,
  })
  const detail = detailQ.data ?? null

  const [descDraft, setDescDraft] = useState('')
  useEffect(() => {
    setDescDraft(detail?.description ?? '')
  }, [detail?.id, detail?.description])

  const saveMut = useMutation({
    mutationFn: saveDescription,
    onSuccess: (res, vars) => {
      if (res.success) success(saveOpName(vars))
      else error(res.message || 'Save failed')
      qc.invalidateQueries({ queryKey: ['schema', 'detail', tree, node?.id] })
      qc.invalidateQueries({ queryKey: ['schema', 'children'] })
    },
    onError: (e) => error(e instanceof Error ? e.message : 'Save failed'),
  })

  const fieldCols: Column<DetailField>[] = useMemo(
    () => [
      { key: 'ord', header: '#', width: '30px', cell: (f) => <span className="font-mono text-2xs text-text3">{f.ordinal}</span> },
      {
        key: 'name',
        header: 'Name',
        width: 'minmax(0,1fr)',
        cell: (f) => (
          <div className="flex min-w-0 flex-col">
            <span className="break-all font-mono text-xs font-semibold">{f.name}</span>
            {f.extra && <span className="font-mono text-2xs text-text3">{f.extra}</span>}
          </div>
        ),
      },
      {
        key: 'type',
        header: 'Type',
        width: 'minmax(0,1fr)',
        cell: (f) => <span className="break-all font-mono text-xs text-accent">{f.type}</span>,
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
    [],
  )

  if (!node) {
    return (
      <EmptyState
        className="mt-2"
        title="No node selected"
        description="Pick a module, object, function or field to inspect it."
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

  const saveNodeDesc = () => {
    if (!detail.saveKind) return
    const input: SaveDescriptionInput = {
      kind: detail.saveKind,
      target: detail.saveKind === 'field' ? { name: detail.name, typeName: detail.typeName } : { name: detail.name },
      description: descDraft,
    }
    saveMut.mutate(input)
  }

  return (
    <div className="flex flex-col gap-3">
      {/* header */}
      <div className="flex flex-col gap-1.5">
        <div className="flex items-center gap-2">
          {chip && (
            <Badge tone={chip.tone} className="px-1.5 py-0 text-[9px]">
              {chip.label}
            </Badge>
          )}
          <span className="break-all font-mono text-base font-bold">{detail.name}</span>
        </div>
        {detail.badges.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {detail.badges.map((b, i) => (
              <Badge key={i} tone={b.tone as Tone}>
                {b.text}
              </Badge>
            ))}
          </div>
        )}
        <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-2xs text-text3">
          {detail.meta && <span>{detail.meta}</span>}
          {detail.primaryKey && detail.primaryKey.length > 0 && (
            <span>
              pk: <span className="font-mono text-text2">{detail.primaryKey.join(', ')}</span>
            </span>
          )}
        </div>
      </div>

      {/* description editor */}
      <div className="flex flex-col gap-1.5">
        <span className="text-xs font-semibold text-text2">Description</span>
        {canSave ? (
          <>
            <Textarea
              rows={5}
              value={descDraft}
              onChange={(e) => setDescDraft(e.target.value)}
              placeholder="Describe this for humans and agents — descriptions feed LLM schema summaries."
            />
            <div className="flex justify-end">
              <Button variant="primary" size="sm" disabled={saveMut.isPending} onClick={saveNodeDesc}>
                {saveMut.isPending ? 'Saving…' : 'Save description'}
              </Button>
            </div>
          </>
        ) : (
          <span className="text-xs text-text3">This node has no editable description.</span>
        )}
      </div>

      {/* arguments */}
      {detail.args && detail.args.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <span className="text-sm font-bold">Arguments ({detail.args.length})</span>
          <DataTable columns={fieldCols} rows={detail.args} getKey={(f) => f.name} />
        </div>
      )}

      {/* fields */}
      {detail.fields.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <span className="text-sm font-bold">
            {detail.fieldsLabel ?? 'Fields'} ({detail.fields.length})
          </span>
          <DataTable columns={fieldCols} rows={detail.fields} getKey={(f) => f.name} />
        </div>
      )}

      {/* enum values */}
      {detail.enumValues && detail.enumValues.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <span className="text-sm font-bold">Values ({detail.enumValues.length})</span>
          <div className="overflow-hidden rounded-card border border-border">
            {detail.enumValues.map((ev) => (
              <div key={ev.name} className="flex items-baseline gap-2 border-b border-border px-3 py-1.5 last:border-b-0">
                <span className="font-mono text-xs font-semibold">{ev.name}</span>
                {ev.description && <span className="text-2xs text-text3">{ev.description}</span>}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* relations */}
      {detail.relations.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <span className="text-sm font-bold">Relations ({detail.relations.length})</span>
          <div className="overflow-hidden rounded-card border border-border">
            {detail.relations.map((r, i) => (
              <div
                key={i}
                className="flex items-center gap-2 border-b border-border px-3 py-1.5 text-xs last:border-b-0"
              >
                <Badge tone={r.direction === 'in' ? 'blue' : 'accent'}>{r.direction}</Badge>
                {r.kind && <Badge tone="neutral">{r.kind}</Badge>}
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
