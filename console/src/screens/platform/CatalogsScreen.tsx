import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Page, PageHeader, ApiHint } from '@/components/shell/Page'
import {
  Badge,
  Button,
  DataTable,
  Drawer,
  EmptyState,
  Field,
  Input,
  Menu,
  MenuContent,
  MenuItem,
  MenuTrigger,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Select,
  Spinner,
  useToast,
  type Column,
} from '@/components/ui'
import {
  CATALOG_SOURCE_TYPES,
  deleteCatalogSource,
  insertCatalogSource,
  linkCatalog,
  listCatalogLinks,
  listCatalogSources,
  listDataSources,
  schemaVersionClean,
  unlinkCatalog,
  updateCatalogSource,
  type CatalogSource,
  type CatalogSourceInput,
} from '@/api/platform-sources'

interface CatForm {
  /** Existing catalog name when editing; null when adding. */
  original: string | null
  name: string
  type: string
  path: string
  description: string
}

function emptyCatForm(): CatForm {
  return { original: null, name: '', type: 'localFS', path: '', description: '' }
}

function toForm(c: CatalogSource): CatForm {
  return { original: c.name, name: c.name, type: c.type, path: c.path, description: c.description }
}

export function CatalogsScreen() {
  const qc = useQueryClient()
  const { success, error, toast } = useToast()

  const sources = useQuery({ queryKey: ['catalogSources'], queryFn: listCatalogSources })
  const links = useQuery({ queryKey: ['catalogLinks'], queryFn: listCatalogLinks })
  const dataSources = useQuery({ queryKey: ['dataSources'], queryFn: listDataSources })

  const linksByCatalog = useMemo(() => {
    const m = new Map<string, string[]>()
    for (const l of links.data ?? []) {
      const arr = m.get(l.catalog_name) ?? []
      arr.push(l.data_source_name)
      m.set(l.catalog_name, arr)
    }
    return m
  }, [links.data])
  const allDsNames = useMemo(() => (dataSources.data ?? []).map((d) => d.name), [dataSources.data])

  const onFail = (e: unknown) => error(e instanceof Error ? e.message : String(e))
  const invalidateLinks = () => qc.invalidateQueries({ queryKey: ['catalogLinks'] })
  const invalidateSources = () => qc.invalidateQueries({ queryKey: ['catalogSources'] })

  const link = useMutation({
    mutationFn: (v: { catalog: string; ds: string }) => linkCatalog(v.catalog, v.ds),
    onSuccess: (res) => {
      success(res.message)
      invalidateLinks()
    },
    onError: onFail,
  })
  const unlink = useMutation({
    mutationFn: (v: { catalog: string; ds: string }) => unlinkCatalog(v.catalog, v.ds),
    onSuccess: (res) => {
      toast(res.message, { tone: 'info' })
      invalidateLinks()
    },
    onError: onFail,
  })
  const versionClean = useMutation({
    mutationFn: (name: string) => schemaVersionClean(name),
    onSuccess: (res) => success(res.message),
    onError: onFail,
  })
  const removeSource = useMutation({
    mutationFn: (name: string) => deleteCatalogSource(name),
    onSuccess: (res) => {
      toast(res.message, { tone: 'info' })
      invalidateSources()
      invalidateLinks()
      setForm(null)
    },
    onError: onFail,
  })

  // ── add / edit drawer ──────────────────────────────────────────────────
  const [form, setForm] = useState<CatForm | null>(null)
  const patch = (p: Partial<CatForm>) => setForm((f) => (f ? { ...f, ...p } : f))
  const editing = form?.original !== null

  const save = useMutation({
    mutationFn: (f: CatForm) => {
      const data: CatalogSourceInput = {
        name: f.name.trim(),
        type: f.type,
        path: f.path,
        description: f.description,
      }
      // Rename would orphan links (they key on catalog_name), so name is read-only
      // on edit — send only the mutable fields.
      return f.original === null
        ? insertCatalogSource(data)
        : updateCatalogSource(f.original, { type: f.type, path: f.path, description: f.description })
    },
    onSuccess: (res) => {
      success(res.message)
      invalidateSources()
      setForm(null)
    },
    onError: onFail,
  })

  const loading = sources.isLoading || links.isLoading
  const rows = sources.data ?? []

  const columns: Column<CatalogSource>[] = [
    {
      key: 'name',
      header: 'Name',
      width: 'minmax(0,1fr)',
      cell: (c) => (
        <div className="flex min-w-0 flex-col">
          <button
            type="button"
            onClick={() => setForm(toForm(c))}
            className="truncate text-left font-mono text-xs font-semibold text-text hover:text-accent"
            title={c.name}
          >
            {c.name}
          </button>
          {c.description && (
            <span className="truncate text-2xs text-text3" title={c.description}>
              {c.description}
            </span>
          )}
        </div>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      width: '0.55fr',
      cell: (c) => <Badge tone="neutral">{c.type}</Badge>,
    },
    {
      key: 'path',
      header: 'Path',
      width: 'minmax(0,1.2fr)',
      cell: (c) => (
        <span className="truncate font-mono text-2xs text-text3" title={c.path}>
          {c.path}
        </span>
      ),
    },
    {
      key: 'linked',
      header: 'Linked sources',
      width: 'minmax(0,1.5fr)',
      cell: (c) => {
        const linked = linksByCatalog.get(c.name) ?? []
        const available = allDsNames.filter((n) => !linked.includes(n))
        return (
          <div className="flex flex-wrap items-center gap-1.5">
            {linked.map((n) => (
              <span
                key={n}
                className="inline-flex items-center gap-1.5 rounded-chip bg-accent-soft px-2 py-0.5 font-mono text-2xs font-semibold text-accent"
              >
                {n}
                <button
                  type="button"
                  className="text-accent opacity-70 hover:opacity-100"
                  title="delete_catalogs"
                  aria-label={`unlink ${n}`}
                  onClick={() => unlink.mutate({ catalog: c.name, ds: n })}
                >
                  ✕
                </button>
              </span>
            ))}
            <Popover>
              <PopoverTrigger asChild>
                <button
                  type="button"
                  className="rounded-chip border border-dashed border-border2 px-2 py-0.5 text-2xs font-semibold text-text3 hover:border-accent hover:text-accent"
                >
                  ＋ link
                </button>
              </PopoverTrigger>
              <PopoverContent className="min-w-[180px]">
                {available.length === 0 ? (
                  <div className="px-2 py-1.5 text-2xs text-text3">All data sources linked</div>
                ) : (
                  <div className="flex max-h-[240px] flex-col overflow-y-auto">
                    {available.map((n) => (
                      <button
                        key={n}
                        type="button"
                        className="rounded-[6px] px-2.5 py-1.5 text-left font-mono text-xs hover:bg-surface2"
                        onClick={() => link.mutate({ catalog: c.name, ds: n })}
                      >
                        {n}
                      </button>
                    ))}
                  </div>
                )}
              </PopoverContent>
            </Popover>
          </div>
        )
      },
    },
    {
      key: 'actions',
      header: 'Actions',
      width: '250px',
      align: 'right',
      cell: (c) => (
        <div className="flex items-center justify-end gap-1">
          <Button
            size="sm"
            variant="secondary"
            onClick={() => versionClean.mutate(c.name)}
            title="_schema_version_clean(name)"
          >
            Version clean
          </Button>
          <Menu>
            <MenuTrigger asChild>
              <Button size="icon" variant="ghost" aria-label="more actions">
                ⋯
              </Button>
            </MenuTrigger>
            <MenuContent>
              <MenuItem danger onSelect={() => removeSource.mutate(c.name)}>
                Delete catalog source
              </MenuItem>
            </MenuContent>
          </Menu>
        </div>
      ),
    },
  ]

  return (
    <Page>
      <PageHeader
        title="Catalogs"
        subtitle="Schema definition sources linked to data sources."
        actions={
          <Button variant="primary" size="sm" onClick={() => setForm(emptyCatForm())}>
            ＋ Add catalog source
          </Button>
        }
      />

      {sources.isError ? (
        <EmptyState
          title="Couldn't load catalog sources"
          description={sources.error instanceof Error ? sources.error.message : undefined}
        />
      ) : (
        <DataTable
          columns={columns}
          rows={rows}
          getKey={(c) => c.name}
          empty={
            loading ? (
              <div className="flex justify-center py-6">
                <Spinner size={18} />
              </div>
            ) : (
              <EmptyState
                title="No catalog sources"
                description="Add a schema catalog and link it to one or more data sources."
                action={
                  <Button variant="primary" size="sm" onClick={() => setForm(emptyCatForm())}>
                    ＋ Add catalog source
                  </Button>
                }
              />
            )
          }
        />
      )}

      <ApiHint>core.catalog_sources / core.catalogs</ApiHint>

      {/* ── add / edit drawer ── */}
      <Drawer
        open={!!form}
        onOpenChange={(o) => !o && setForm(null)}
        title={form?.original === null ? 'Add catalog source' : `Edit ${form?.original ?? ''}`}
        subtitle={
          form?.original === null
            ? 'insert_catalog_sources(data:{…})'
            : `update_catalog_sources(filter:{name:{eq:"${form?.original ?? ''}"}})`
        }
        width={440}
        footer={
          form && (
            <>
              {editing && (
                <Button
                  variant="danger-ghost"
                  size="sm"
                  className="mr-auto"
                  disabled={removeSource.isPending}
                  onClick={() => removeSource.mutate(form.original as string)}
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
                {editing ? 'Save' : 'Create'}
              </Button>
            </>
          )
        }
      >
        {form && (
          <div className="flex flex-col gap-3">
            <div className="grid grid-cols-[1fr_120px] gap-2.5">
              <Field label="name">
                <Input
                  mono
                  value={form.name}
                  disabled={editing}
                  onChange={(e) => patch({ name: e.target.value })}
                  placeholder="mesh-core"
                />
              </Field>
              <Field label="type">
                <Select value={form.type} onChange={(e) => patch({ type: e.target.value })}>
                  {CATALOG_SOURCE_TYPES.map((t) => (
                    <option key={t} value={t}>
                      {t}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>
            <Field label="path">
              <Input
                mono
                value={form.path}
                onChange={(e) => patch({ path: e.target.value })}
                placeholder="/schemas/core"
              />
            </Field>
            <Field label="description">
              <Input value={form.description} onChange={(e) => patch({ description: e.target.value })} />
            </Field>
            {editing &&
              (() => {
                const cat = form.original as string
                const linked = linksByCatalog.get(cat) ?? []
                const available = allDsNames.filter((n) => !linked.includes(n))
                return (
                  <div className="flex flex-col gap-1.5">
                    <span className="text-xs font-medium text-text2">Linked data sources</span>
                    <div className="flex flex-wrap items-center gap-1.5">
                      {linked.map((n) => (
                        <span
                          key={n}
                          className="inline-flex items-center gap-1.5 rounded-chip bg-accent-soft px-2 py-0.5 font-mono text-2xs font-semibold text-accent"
                        >
                          {n}
                          <button
                            type="button"
                            className="text-accent opacity-70 hover:opacity-100"
                            aria-label={`unlink ${n}`}
                            onClick={() => unlink.mutate({ catalog: cat, ds: n })}
                          >
                            ✕
                          </button>
                        </span>
                      ))}
                      <Popover>
                        <PopoverTrigger asChild>
                          <button
                            type="button"
                            className="rounded-chip border border-dashed border-border2 px-2 py-0.5 text-2xs font-semibold text-text3 hover:border-accent hover:text-accent"
                          >
                            ＋ link
                          </button>
                        </PopoverTrigger>
                        <PopoverContent className="min-w-[180px]">
                          {available.length === 0 ? (
                            <div className="px-2 py-1.5 text-2xs text-text3">All data sources linked</div>
                          ) : (
                            <div className="flex max-h-[240px] flex-col overflow-y-auto">
                              {available.map((n) => (
                                <button
                                  key={n}
                                  type="button"
                                  className="rounded-[6px] px-2.5 py-1.5 text-left font-mono text-xs hover:bg-surface2"
                                  onClick={() => link.mutate({ catalog: cat, ds: n })}
                                >
                                  {n}
                                </button>
                              ))}
                            </div>
                          )}
                        </PopoverContent>
                      </Popover>
                    </div>
                  </div>
                )
              })()}
            {editing && (
              <p className="font-mono text-2xs text-text3">
                Renaming a catalog would orphan its links — create a new source instead.
              </p>
            )}
          </div>
        )}
      </Drawer>
    </Page>
  )
}
