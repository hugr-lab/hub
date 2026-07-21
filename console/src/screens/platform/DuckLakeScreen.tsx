import { useMemo, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { cn } from '@/lib/cn'
import { ApiHint } from '@/components/shell/Page'
import {
  Badge,
  Button,
  DataTable,
  Drawer,
  EmptyState,
  Field,
  Input,
  SearchField,
  Segmented,
  Select,
  Spinner,
  Toggle,
  useToast,
  type Column,
} from '@/components/ui'
import { listDataSources, type FnResult } from '@/api/platform-sources'
import {
  checkpoint,
  cleanupOldFiles,
  DUCKLAKE_OPTIONS,
  expireSnapshots,
  fetchDataFiles,
  fetchSnapshots,
  fetchTableStats,
  flushInlinedData,
  mergeAdjacentFiles,
  rewriteDataFiles,
  setOption,
  type DuckLakeOption,
  type DuckLakeTableStat,
  type DuckLakeSnapshot,
} from '@/api/platform-ducklake'

type Tab = 'tables' | 'snapshots' | 'maintenance' | 'options'
const SNAP_PAGE = 50

export function DuckLakeScreen() {
  const { success, error } = useToast()
  const onFail = (e: unknown) => error(e instanceof Error ? e.message : String(e))
  const onFn = (r: FnResult) => (r.success ? success(r.message) : error(r.message))

  // DuckLake sources = data_sources of type `ducklake` (shares the cache).
  const sources = useQuery({ queryKey: ['dataSources'], queryFn: listDataSources })
  const lakes = useMemo(
    () => (sources.data ?? []).filter((d) => d.type === 'ducklake').map((d) => d.name),
    [sources.data],
  )
  const [selected, setSelected] = useState<string | null>(null)
  const name = selected && lakes.includes(selected) ? selected : (lakes[0] ?? null)
  // Maintenance opens first; Tables / Snapshots fetch lazily on their own tab.
  const [tab, setTab] = useState<Tab>('maintenance')

  return (
    <div className="flex min-h-0 flex-1">
      {/* master: ducklake source list */}
      <aside className="flex w-[230px] flex-none flex-col overflow-y-auto border-r border-border bg-surface">
        <div className="border-b border-border px-4 py-3">
          <h1 className="text-base font-semibold tracking-[-0.01em]">DuckLake</h1>
          <p className="mt-0.5 text-xs text-text3">Snapshots · files · maintenance</p>
        </div>
        <div className="flex flex-col gap-0.5 p-2">
          {sources.isLoading ? (
            <div className="py-6">
              <Center />
            </div>
          ) : lakes.length === 0 ? (
            <p className="px-2 py-3 text-xs text-text3">No DuckLake sources.</p>
          ) : (
            lakes.map((n) => (
              <button
                key={n}
                type="button"
                onClick={() => {
                  setSelected(n)
                  setTab('maintenance')
                }}
                className={cn(
                  'truncate rounded-[7px] px-2.5 py-2 text-left font-mono text-xs font-semibold',
                  n === name ? 'bg-accent-soft text-accent' : 'text-text2 hover:bg-surface2',
                )}
                title={n}
              >
                {n}
              </button>
            ))
          )}
        </div>
      </aside>

      {/* detail: selected source */}
      <section className="flex min-w-0 flex-1 flex-col overflow-hidden">
        {!name ? (
          <div className="flex flex-1 items-center justify-center">
            <EmptyState
              title="No DuckLake sources"
              description="Attach a data source of type `ducklake` to manage it here."
            />
          </div>
        ) : (
          <>
            <div className="flex items-center gap-3 border-b border-border px-[22px] py-2.5">
              <span className="truncate font-mono text-sm font-semibold" title={name}>
                {name}
              </span>
              <span className="flex-1" />
              <Segmented<Tab>
                value={tab}
                onChange={setTab}
                options={[
                  { value: 'maintenance', label: 'Maintenance' },
                  { value: 'tables', label: 'Tables' },
                  { value: 'snapshots', label: 'Snapshots' },
                  { value: 'options', label: 'Options' },
                ]}
              />
            </div>

            <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-[22px] py-4">
              {tab === 'tables' && <TablesTab key={`t-${name}`} name={name} />}
              {tab === 'snapshots' && <SnapshotsTab key={`s-${name}`} name={name} />}
              {tab === 'maintenance' && <MaintenanceTab key={`m-${name}`} name={name} onFn={onFn} onFail={onFail} />}
              {tab === 'options' && <OptionsTab key={`o-${name}`} name={name} onFn={onFn} onFail={onFail} />}
              <ApiHint>core.ducklake · function.core.ducklake</ApiHint>
            </div>
          </>
        )}
      </section>
    </div>
  )
}

/* ── Tables ─────────────────────────────────────────────────────────────── */

function TablesTab({ name }: { name: string }) {
  const q = useQuery({ queryKey: ['dlTables', name], queryFn: () => fetchTableStats(name) })
  const [search, setSearch] = useState('')
  const [fileFor, setFileFor] = useState<string | null>(null)
  const files = useQuery({
    queryKey: ['dlFiles', name, fileFor],
    queryFn: () => fetchDataFiles(name, fileFor as string),
    enabled: !!fileFor,
  })

  const rows = useMemo(() => {
    const all = q.data ?? []
    const s = search.trim().toLowerCase()
    return s ? all.filter((t) => t.table_name.toLowerCase().includes(s)) : all
  }, [q.data, search])

  const columns: Column<DuckLakeTableStat>[] = [
    {
      key: 'table',
      header: 'Table',
      width: 'minmax(0,1.2fr)',
      cell: (t) => (
        <button
          type="button"
          onClick={() => setFileFor(t.table_name)}
          className="truncate text-left font-mono text-xs font-semibold text-text hover:text-accent"
          title="View data files"
        >
          {t.table_name}
        </button>
      ),
    },
    { key: 'files', header: 'Files', width: '0.5fr', align: 'right', cell: (t) => <Mono>{t.file_count}</Mono> },
    {
      key: 'size',
      header: 'Size',
      width: '0.7fr',
      align: 'right',
      cell: (t) => <Mono>{fmtBytes(t.file_size_bytes)}</Mono>,
    },
    {
      key: 'deletes',
      header: 'Delete files',
      width: '0.8fr',
      align: 'right',
      cell: (t) =>
        t.delete_file_count > 0 ? (
          <span className="font-mono text-xs text-amber">
            {t.delete_file_count} · {fmtBytes(t.delete_file_size_bytes)}
          </span>
        ) : (
          <Mono dim>—</Mono>
        ),
    },
  ]

  return (
    <>
      <div className="flex items-center gap-2">
        <SearchField
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search tables…"
          className="max-w-xs"
        />
        <span className="text-2xs text-text3">
          {rows.length}
          {q.data && rows.length !== q.data.length ? ` / ${q.data.length}` : ''} tables
        </span>
      </div>
      <DataTable
        columns={columns}
        rows={rows}
        getKey={(t) => t.table_name}
        empty={
          q.isLoading ? (
            <Center />
          ) : (
            <EmptyState title="No tables" description={search ? 'No table matches the search.' : 'This DuckLake has no tables.'} />
          )
        }
      />

      <Drawer
        open={!!fileFor}
        onOpenChange={(o) => !o && setFileFor(null)}
        title={fileFor ?? ''}
        subtitle="data_files(name, table_name)"
        width={620}
      >
        {files.isLoading ? (
          <Center />
        ) : (files.data ?? []).length === 0 ? (
          <EmptyState title="No data files" description="This table has no materialised files." />
        ) : (
          <div className="flex flex-col gap-2">
            {(files.data ?? []).map((f) => (
              <div key={f.data_file} className="flex flex-col gap-1 rounded-card border border-border px-3 py-2">
                <div className="flex items-baseline gap-2">
                  <span className="truncate font-mono text-2xs text-text2" title={f.data_file}>
                    {f.data_file}
                  </span>
                  <span className="flex-1" />
                  <span className="shrink-0 font-mono text-2xs text-text3">{fmtBytes(f.data_file_size_bytes)}</span>
                </div>
                {f.delete_file && (
                  <div className="flex items-baseline gap-2">
                    <Badge tone="amber">delete</Badge>
                    <span className="truncate font-mono text-2xs text-text3" title={f.delete_file}>
                      {f.delete_file}
                    </span>
                    <span className="flex-1" />
                    <span className="shrink-0 font-mono text-2xs text-text3">
                      {fmtBytes(f.delete_file_size_bytes ?? 0)}
                    </span>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </Drawer>
    </>
  )
}

/* ── Snapshots (server-paged, newest first) ─────────────────────────────── */

function SnapshotsTab({ name }: { name: string }) {
  const [page, setPage] = useState(0)
  const q = useQuery({
    queryKey: ['dlSnapshots', name, page],
    queryFn: () => fetchSnapshots(name, SNAP_PAGE, page * SNAP_PAGE),
  })
  const rows = q.data ?? []
  const hasNext = rows.length === SNAP_PAGE

  const columns: Column<DuckLakeSnapshot>[] = [
    { key: 'id', header: 'ID', width: '0.4fr', cell: (s) => <Mono>{s.snapshot_id}</Mono> },
    { key: 'time', header: 'Time', width: 'minmax(0,1fr)', cell: (s) => <Mono dim>{fmtTime(s.snapshot_time)}</Mono> },
    { key: 'schema', header: 'Schema v', width: '0.5fr', align: 'right', cell: (s) => <Mono>{s.schema_version}</Mono> },
    {
      key: 'changes',
      header: 'Changes',
      width: 'minmax(0,1.4fr)',
      cell: (s) => (
        <span className="truncate font-mono text-2xs text-text2" title={s.changes}>
          {summarizeChanges(s.changes)}
        </span>
      ),
    },
  ]

  return (
    <>
      <DataTable
        columns={columns}
        rows={rows}
        getKey={(s) => String(s.snapshot_id)}
        empty={q.isLoading ? <Center /> : <EmptyState title="No snapshots" description="No snapshot history on this page." />}
      />
      <div className="flex items-center gap-2">
        <Button size="sm" variant="secondary" disabled={page === 0 || q.isFetching} onClick={() => setPage((p) => Math.max(0, p - 1))}>
          ← Prev
        </Button>
        <span className="text-2xs text-text3">page {page + 1}</span>
        <Button size="sm" variant="secondary" disabled={!hasNext || q.isFetching} onClick={() => setPage((p) => p + 1)}>
          Next →
        </Button>
        {q.isFetching && <Spinner size={14} />}
      </div>
    </>
  )
}

/* ── Maintenance ────────────────────────────────────────────────────────── */

function MaintenanceTab({
  name,
  onFn,
  onFail,
}: {
  name: string
  onFn: (r: FnResult) => void
  onFail: (e: unknown) => void
}) {
  const run = (fn: () => Promise<FnResult>) => fn().then(onFn).catch(onFail)
  const [expireDry, setExpireDry] = useState(true)
  const [expireOlder, setExpireOlder] = useState('')
  const [cleanupDry, setCleanupDry] = useState(true)
  const [cleanupOlder, setCleanupOlder] = useState('')
  const [cleanupAll, setCleanupAll] = useState(false)

  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-3">
      <Card title="Checkpoint" desc="Flush the WAL and write a consistent checkpoint.">
        <Button size="sm" variant="secondary" onClick={() => run(() => checkpoint(name))}>
          Run checkpoint
        </Button>
      </Card>

      <Card title="Flush inlined data" desc="Materialise inlined rows into data files.">
        <Button size="sm" variant="secondary" onClick={() => run(() => flushInlinedData(name))}>
          Flush
        </Button>
      </Card>

      <Card title="Merge adjacent files" desc="Compact small adjacent files across the source.">
        <Button size="sm" variant="secondary" onClick={() => run(() => mergeAdjacentFiles(name))}>
          Merge
        </Button>
      </Card>

      <Card title="Rewrite data files" desc="Rewrite files to apply pending deletes.">
        <Button size="sm" variant="secondary" onClick={() => run(() => rewriteDataFiles(name))}>
          Rewrite
        </Button>
      </Card>

      <Card title="Expire snapshots" desc="Drop old snapshots (time-travel history). Dry-run previews.">
        <div className="flex flex-col gap-2">
          <Field label="older_than (optional, e.g. 7d / timestamp)">
            <Input mono value={expireOlder} onChange={(e) => setExpireOlder(e.target.value)} placeholder="7d" />
          </Field>
          <label className="flex cursor-pointer items-center gap-2 text-xs">
            <Toggle checked={expireDry} onCheckedChange={setExpireDry} />
            dry run
          </label>
          <Button
            size="sm"
            variant={expireDry ? 'secondary' : 'danger'}
            onClick={() => run(() => expireSnapshots(name, { older_than: expireOlder || undefined, dry_run: expireDry }))}
          >
            {expireDry ? 'Preview expire' : 'Expire snapshots'}
          </Button>
        </div>
      </Card>

      <Card title="Cleanup old files" desc="Delete orphaned files no snapshot references. Dry-run previews.">
        <div className="flex flex-col gap-2">
          <Field label="older_than (optional)">
            <Input mono value={cleanupOlder} onChange={(e) => setCleanupOlder(e.target.value)} placeholder="7d" />
          </Field>
          <label className="flex cursor-pointer items-center gap-2 text-xs">
            <Toggle checked={cleanupAll} onCheckedChange={setCleanupAll} />
            cleanup_all
          </label>
          <label className="flex cursor-pointer items-center gap-2 text-xs">
            <Toggle checked={cleanupDry} onCheckedChange={setCleanupDry} />
            dry run
          </label>
          <Button
            size="sm"
            variant={cleanupDry ? 'secondary' : 'danger'}
            onClick={() =>
              run(() =>
                cleanupOldFiles(name, {
                  older_than: cleanupOlder || undefined,
                  cleanup_all: cleanupAll,
                  dry_run: cleanupDry,
                }),
              )
            }
          >
            {cleanupDry ? 'Preview cleanup' : 'Cleanup files'}
          </Button>
        </div>
      </Card>
    </div>
  )
}

/* ── Options (source-wide or per-table) ─────────────────────────────────── */

function OptionsTab({
  name,
  onFn,
  onFail,
}: {
  name: string
  onFn: (r: FnResult) => void
  onFail: (e: unknown) => void
}) {
  const tables = useQuery({ queryKey: ['dlTables', name], queryFn: () => fetchTableStats(name) })
  const [scope, setScope] = useState<'source' | 'table'>('source')
  const [table, setTable] = useState('')
  const [option, setOptionName] = useState<DuckLakeOption>(DUCKLAKE_OPTIONS[0])
  const [value, setValue] = useState('')

  const tableNames = (tables.data ?? []).map((t) => t.table_name)
  const effectiveTable = table || tableNames[0] || ''

  const save = useMutation({
    mutationFn: () => setOption(name, option, value, scope === 'table' ? { table_name: effectiveTable } : {}),
    onSuccess: onFn,
    onError: onFail,
  })

  return (
    <div className="max-w-lg">
      <Card title="Set option" desc="Set a DuckLake option source-wide or on a single table. Value format depends on the option.">
        <div className="flex flex-col gap-2.5">
          <Segmented<'source' | 'table'>
            value={scope}
            onChange={setScope}
            options={[
              { value: 'source', label: 'Whole source' },
              { value: 'table', label: 'Table' },
            ]}
          />
          {scope === 'table' && (
            <Field label="table">
              <Select value={effectiveTable} onChange={(e) => setTable(e.target.value)}>
                {tableNames.length === 0 && <option value="">— no tables —</option>}
                {tableNames.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          <Field label="option">
            <Select value={option} onChange={(e) => setOptionName(e.target.value as DuckLakeOption)}>
              {DUCKLAKE_OPTIONS.map((o) => (
                <option key={o} value={o}>
                  {o}
                </option>
              ))}
            </Select>
          </Field>
          <Field label="value">
            <Input mono value={value} onChange={(e) => setValue(e.target.value)} placeholder="e.g. 128MB / true / 7d" />
          </Field>
          <Button
            size="sm"
            variant="primary"
            disabled={save.isPending || !value.trim() || (scope === 'table' && !effectiveTable)}
            onClick={() => save.mutate()}
          >
            Set option
          </Button>
        </div>
      </Card>
    </div>
  )
}

/* ── small helpers ──────────────────────────────────────────────────────── */

function Card({ title, desc, children }: { title: string; desc: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2.5 rounded-card border border-border bg-surface p-4">
      <div className="flex flex-col gap-0.5">
        <span className="text-sm font-semibold">{title}</span>
        <span className="text-2xs leading-relaxed text-text3">{desc}</span>
      </div>
      {children}
    </div>
  )
}

function Mono({ children, dim }: { children: React.ReactNode; dim?: boolean }) {
  return <span className={`font-mono text-xs ${dim ? 'text-text3' : 'text-text2'}`}>{children}</span>
}

function Center() {
  return (
    <div className="flex justify-center py-6">
      <Spinner size={18} />
    </div>
  )
}

function fmtBytes(n: number): string {
  if (!n) return '0'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n
  let i = 0
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 && i > 0 ? 1 : 0)} ${u[i]}`
}

function fmtTime(iso: string): string {
  return iso.replace(/\.\d+/, '').replace('T', ' ')
}

/** Turn the snapshot `changes` JSON into a short readable label. */
function summarizeChanges(raw: string): string {
  try {
    const o = JSON.parse(raw) as Record<string, unknown>
    const parts = Object.entries(o).map(([k, v]) => {
      const label = k.replace(/_/g, ' ')
      const items = Array.isArray(v) ? v.join(', ') : String(v)
      return `${label}: ${items}`
    })
    return parts.length ? parts.join(' · ') : '—'
  } catch {
    return raw
  }
}
