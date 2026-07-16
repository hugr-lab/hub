import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Page, PageHeader, ApiHint } from '@/components/shell/Page'
import {
  Badge,
  Button,
  CheckboxBox,
  DataTable,
  Dot,
  Drawer,
  EmptyState,
  Field,
  Input,
  Select,
  Spinner,
  useToast,
  type Column,
} from '@/components/ui'
import {
  checkpointDataSource,
  DATA_SOURCE_TYPES,
  deleteDataSource,
  describeDataSourceSchema,
  fetchDataSourceStatuses,
  insertDataSource,
  listDataSources,
  loadDataSource,
  schemaReindex,
  unloadDataSource,
  updateDataSource,
  type DataSource,
  type DataSourceStatus,
  type FnResult,
  type NestedCatalog,
} from '@/api/platform-sources'

interface DsForm {
  original: string | null
  name: string
  type: string
  prefix: string
  path: string
  description: string
  as_module: boolean
  read_only: boolean
  self_defined: boolean
  catalogs: NestedCatalog[]
}

function emptyForm(): DsForm {
  return {
    original: null,
    name: '',
    type: 'postgres',
    prefix: '',
    path: '',
    description: '',
    as_module: true,
    read_only: false,
    self_defined: false,
    catalogs: [],
  }
}

export function DataSourcesScreen() {
  const qc = useQueryClient()
  const { success, error } = useToast()

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['dataSources'] })
    qc.invalidateQueries({ queryKey: ['dataSourceStatuses'] })
  }

  const sources = useQuery({ queryKey: ['dataSources'], queryFn: listDataSources })
  const names = useMemo(() => (sources.data ?? []).map((d) => d.name), [sources.data])

  const statuses = useQuery({
    queryKey: ['dataSourceStatuses'],
    queryFn: () => fetchDataSourceStatuses(names),
    enabled: names.length > 0,
    refetchInterval: (query) => {
      const data = query.state.data as Record<string, DataSourceStatus> | undefined
      return data && Object.values(data).some((s) => s === 'loading') ? 1200 : false
    },
  })
  const statusOf = (name: string): DataSourceStatus => statuses.data?.[name] ?? 'unloaded'

  // ── mutations ──────────────────────────────────────────────────────────
  const onFn = (res: FnResult) => {
    success(res.message)
    invalidate()
  }
  const onFail = (e: unknown) => error(e instanceof Error ? e.message : String(e))

  const setStatus = (name: string, s: DataSourceStatus) =>
    qc.setQueryData<Record<string, DataSourceStatus>>(['dataSourceStatuses'], (prev) => ({
      ...(prev ?? {}),
      [name]: s,
    }))

  const load = useMutation({
    mutationFn: (name: string) => loadDataSource(name),
    onMutate: (name) => setStatus(name, 'loading'),
    onSuccess: onFn,
    onError: onFail,
  })
  const unload = useMutation({
    mutationFn: (name: string) => unloadDataSource(name, false),
    onMutate: (name) => setStatus(name, 'unloaded'),
    onSuccess: onFn,
    onError: onFail,
  })
  const checkpoint = useMutation({
    mutationFn: (name: string) => checkpointDataSource(name),
    onSuccess: (res) => success(res.message),
    onError: onFail,
  })
  const reindex = useMutation({
    mutationFn: (name: string) => schemaReindex(name),
    onSuccess: (res) => success(res.message),
    onError: onFail,
  })

  // ── add / edit drawer ──────────────────────────────────────────────────
  const [form, setForm] = useState<DsForm | null>(null)
  const patch = (p: Partial<DsForm>) => setForm((f) => (f ? { ...f, ...p } : f))

  const save = useMutation({
    mutationFn: (f: DsForm) => {
      const data = {
        name: f.name.trim(),
        type: f.type,
        prefix: f.prefix,
        path: f.path,
        description: f.description,
        as_module: f.as_module,
        read_only: f.read_only,
        self_defined: f.self_defined,
      }
      return f.original === null
        ? insertDataSource({ ...data, catalogs: f.catalogs.length ? f.catalogs : undefined })
        : updateDataSource(f.original, data)
    },
    onSuccess: (res) => {
      success(res.message)
      invalidate()
      setForm(null)
    },
    onError: onFail,
  })

  const remove = useMutation({
    mutationFn: (name: string) => deleteDataSource(name),
    onSuccess: (res) => {
      success(res.message)
      invalidate()
      setForm(null)
    },
    onError: onFail,
  })

  // ── describe schema drawer ─────────────────────────────────────────────
  const [schemaFor, setSchemaFor] = useState<string | null>(null)
  const schema = useQuery({
    queryKey: ['dsSchema', schemaFor],
    queryFn: () => describeDataSourceSchema(schemaFor as string),
    enabled: !!schemaFor,
  })

  // ── columns ────────────────────────────────────────────────────────────
  const columns: Column<DataSource>[] = [
    {
      key: 'dot',
      header: '',
      width: '16px',
      cell: (d) => <Dot state={statusOf(d.name)} size={8} />,
    },
    {
      key: 'name',
      header: 'Name',
      width: 'minmax(0,1.1fr)',
      cell: (d) => (
        <div className="flex min-w-0 flex-col">
          <button
            type="button"
            onClick={() => setForm({ ...toForm(d) })}
            className="truncate text-left font-mono text-xs font-semibold text-text hover:text-accent"
            title={d.name}
          >
            {d.name}
          </button>
          <span className="truncate text-2xs text-text3" title={d.description}>
            {d.description}
          </span>
        </div>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      width: '0.7fr',
      cell: (d) => <span className="truncate font-mono text-xs text-text2">{d.type}</span>,
    },
    {
      key: 'prefix',
      header: 'Prefix',
      width: '0.6fr',
      cell: (d) => <span className="truncate font-mono text-xs text-text2">{d.prefix || '—'}</span>,
    },
    {
      key: 'path',
      header: 'Path / DSN',
      width: 'minmax(0,1.4fr)',
      cell: (d) => (
        <span className="truncate font-mono text-2xs text-text3" title={d.path}>
          {d.path}
        </span>
      ),
    },
    {
      key: 'flags',
      header: 'Flags',
      width: 'minmax(0,0.9fr)',
      cell: (d) => (
        <div className="flex flex-wrap gap-1">
          {d.as_module && <Badge tone="blue">module</Badge>}
          {d.read_only && <Badge tone="neutral">read-only</Badge>}
          {d.disabled && <Badge tone="amber">disabled</Badge>}
        </div>
      ),
    },
    {
      key: 'actions',
      header: 'Actions',
      width: '250px',
      align: 'right',
      cell: (d) => {
        const st = statusOf(d.name)
        const isLoad = st === 'unloaded' || st === 'error'
        return (
          <div className="flex items-center justify-end gap-1">
            <Button
              size="sm"
              variant={isLoad ? 'green' : 'amber'}
              disabled={st === 'loading'}
              onClick={() => (isLoad ? load.mutate(d.name) : unload.mutate(d.name))}
            >
              {isLoad ? 'Load' : 'Unload'}
            </Button>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => reindex.mutate(d.name)}
              title="_schema_reindex(name, batch_size) — generated catalog"
            >
              Reindex
            </Button>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => checkpoint.mutate(d.name)}
              title="checkpoint(name)"
            >
              CP
            </Button>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => setSchemaFor(d.name)}
              title="describe_data_source_schema(name)"
            >
              Schema
            </Button>
          </div>
        )
      },
    },
  ]

  const editing = form?.original !== null
  const drawerOp =
    form?.original === null
      ? 'insert_data_sources(data:{…})'
      : `update_data_sources(filter:{name:{eq:"${form?.original ?? ''}"}})`

  return (
    <Page>
      <PageHeader
        title="Data Sources"
        subtitle="Attached databases, lakehouses, files, and LLM sources."
        actions={
          <Button variant="primary" size="sm" onClick={() => setForm(emptyForm())}>
            ＋ Add data source
          </Button>
        }
      />

      {sources.isError ? (
        <EmptyState
          title="Couldn't load data sources"
          description={sources.error instanceof Error ? sources.error.message : undefined}
        />
      ) : (
        <DataTable
          columns={columns}
          rows={sources.data ?? []}
          getKey={(d) => d.name}
          empty={
            sources.isLoading ? (
              <div className="flex justify-center py-6">
                <Spinner size={18} />
              </div>
            ) : (
              <EmptyState
                title="No data sources"
                description="Attach a database, lakehouse, file store, or LLM source."
                action={
                  <Button variant="primary" size="sm" onClick={() => setForm(emptyForm())}>
                    ＋ Add data source
                  </Button>
                }
              />
            )
          }
        />
      )}

      <ApiHint>core.data_sources · data_source_status(name)</ApiHint>

      {/* ── add / edit drawer ── */}
      <Drawer
        open={!!form}
        onOpenChange={(o) => !o && setForm(null)}
        title={form?.original === null ? 'Add data source' : `Edit ${form?.original ?? ''}`}
        subtitle={form ? drawerOp : undefined}
        width={460}
        footer={
          form && (
            <>
              {editing && (
                <Button
                  variant="danger-ghost"
                  size="sm"
                  className="mr-auto"
                  disabled={remove.isPending}
                  onClick={() => remove.mutate(form.original as string)}
                >
                  Delete
                </Button>
              )}
              <Button variant="secondary" size="sm" onClick={() => setForm(null)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                size="sm"
                disabled={!form.name.trim() || save.isPending}
                onClick={() => save.mutate(form)}
              >
                Save
              </Button>
            </>
          )
        }
      >
        {form && (
          <div className="flex flex-col gap-3">
            <div className="grid grid-cols-2 gap-2.5">
              <Field label="name">
                <Input
                  mono
                  value={form.name}
                  onChange={(e) => patch({ name: e.target.value })}
                  placeholder="my-source"
                />
              </Field>
              <Field label="type">
                <Select value={form.type} onChange={(e) => patch({ type: e.target.value })}>
                  {DATA_SOURCE_TYPES.map((t) => (
                    <option key={t} value={t}>
                      {t}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>

            <Field label="path / DSN">
              <Input
                mono
                value={form.path}
                onChange={(e) => patch({ path: e.target.value })}
                placeholder="postgres://user@host:5432/db"
              />
            </Field>

            <div className="grid grid-cols-2 gap-2.5">
              <Field label="prefix">
                <Input mono value={form.prefix} onChange={(e) => patch({ prefix: e.target.value })} />
              </Field>
              <Field label="description">
                <Input
                  value={form.description}
                  onChange={(e) => patch({ description: e.target.value })}
                />
              </Field>
            </div>

            <div className="flex flex-wrap gap-4 py-1">
              <label className="flex cursor-pointer items-center gap-2 text-sm">
                <CheckboxBox checked={form.as_module} onCheckedChange={(v) => patch({ as_module: v })} />
                as_module
              </label>
              <label className="flex cursor-pointer items-center gap-2 text-sm">
                <CheckboxBox checked={form.read_only} onCheckedChange={(v) => patch({ read_only: v })} />
                read_only
              </label>
              <label className="flex cursor-pointer items-center gap-2 text-sm">
                <CheckboxBox
                  checked={form.self_defined}
                  onCheckedChange={(v) => patch({ self_defined: v })}
                />
                self_defined
              </label>
            </div>

            {form.original === null && (
              <div className="flex flex-col gap-2">
                <div className="flex items-center gap-2">
                  <span className="text-xs font-medium text-text2">Nested catalogs (optional)</span>
                  <span className="flex-1" />
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() =>
                      patch({ catalogs: [...form.catalogs, { name: '', type: 'localFS', path: '' }] })
                    }
                  >
                    ＋ add
                  </Button>
                </div>
                {form.catalogs.length === 0 ? (
                  <div className="rounded-card border border-dashed border-border2 px-3 py-2.5 font-mono text-2xs text-text3">
                    catalogs: [ … ] — attach schema catalogs on insert
                  </div>
                ) : (
                  <div className="flex flex-col gap-2">
                    {form.catalogs.map((c, i) => (
                      <div key={i} className="grid grid-cols-[1fr_88px_1.2fr_auto] items-center gap-2">
                        <Input
                          mono
                          placeholder="name"
                          value={c.name}
                          onChange={(e) => patchCatalog(form, patch, i, { name: e.target.value })}
                        />
                        <Select
                          value={c.type}
                          onChange={(e) => patchCatalog(form, patch, i, { type: e.target.value })}
                        >
                          <option value="localFS">localFS</option>
                          <option value="uri">uri</option>
                        </Select>
                        <Input
                          mono
                          placeholder="path"
                          value={c.path}
                          onChange={(e) => patchCatalog(form, patch, i, { path: e.target.value })}
                        />
                        <Button
                          size="icon"
                          variant="ghost"
                          onClick={() =>
                            patch({ catalogs: form.catalogs.filter((_, j) => j !== i) })
                          }
                          aria-label="remove catalog"
                        >
                          ✕
                        </Button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </Drawer>

      {/* ── describe schema drawer ── */}
      <Drawer
        open={!!schemaFor}
        onOpenChange={(o) => !o && setSchemaFor(null)}
        title={schemaFor ?? ''}
        subtitle="describe_data_source_schema(name)"
        width={440}
      >
        {schema.isLoading ? (
          <div className="flex justify-center py-8">
            <Spinner size={18} />
          </div>
        ) : schema.isError ? (
          <EmptyState
            title="Couldn't describe schema"
            description={schema.error instanceof Error ? schema.error.message : undefined}
          />
        ) : (schema.data ?? []).length === 0 ? (
          <EmptyState title="No schema" description="This source exposes no types yet." />
        ) : (
          <div className="flex flex-col gap-2">
            {(schema.data ?? []).map((t) => (
              <div
                key={t.name}
                className="flex flex-col gap-1.5 rounded-card border border-border px-3 py-2.5"
              >
                <div className="flex items-baseline gap-2">
                  <span className="font-mono text-xs font-semibold">{t.name}</span>
                  <Badge tone="neutral">{t.kind}</Badge>
                  <span className="flex-1" />
                  <span className="text-2xs text-text3">{t.count} fields</span>
                </div>
                <div className="font-mono text-2xs leading-relaxed text-text3">{t.fields}</div>
              </div>
            ))}
          </div>
        )}
      </Drawer>
    </Page>
  )
}

function toForm(d: DataSource): DsForm {
  return {
    original: d.name,
    name: d.name,
    type: d.type,
    prefix: d.prefix,
    path: d.path,
    description: d.description,
    as_module: d.as_module,
    read_only: d.read_only,
    self_defined: d.self_defined,
    catalogs: [],
  }
}

function patchCatalog(
  form: DsForm,
  patch: (p: Partial<DsForm>) => void,
  i: number,
  p: Partial<NestedCatalog>,
) {
  patch({ catalogs: form.catalogs.map((c, j) => (j === i ? { ...c, ...p } : c)) })
}
