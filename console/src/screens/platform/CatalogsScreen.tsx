import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Page, PageHeader, ApiHint } from '@/components/shell/Page'
import {
  Badge,
  Button,
  EmptyState,
  Field,
  Input,
  Menu,
  MenuContent,
  MenuItem,
  MenuSeparator,
  MenuTrigger,
  Modal,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Select,
  Spinner,
  useToast,
} from '@/components/ui'
import {
  deleteCatalogSource,
  insertCatalogSource,
  linkCatalog,
  listCatalogLinks,
  listCatalogSources,
  listDataSources,
  schemaHardRemove,
  schemaReindex,
  schemaVersionClean,
  unlinkCatalog,
  type CatalogSourceInput,
  type FnResult,
} from '@/api/platform-sources'

interface CatForm {
  name: string
  type: string
  path: string
  description: string
}

function emptyCatForm(): CatForm {
  return { name: '', type: 'localFS', path: '', description: '' }
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
  const reindex = useMutation({
    mutationFn: (name: string) => schemaReindex(name),
    onSuccess: (res: FnResult) => success(res.message),
    onError: onFail,
  })
  const versionClean = useMutation({
    mutationFn: (name: string) => schemaVersionClean(name),
    onSuccess: (res: FnResult) => success(res.message),
    onError: onFail,
  })
  const hardRemove = useMutation({
    mutationFn: (name: string) => schemaHardRemove(name),
    onSuccess: (res: FnResult) => {
      toast(res.message, { tone: 'info' })
      invalidateSources()
      invalidateLinks()
    },
    onError: onFail,
  })
  const removeSource = useMutation({
    mutationFn: (name: string) => deleteCatalogSource(name),
    onSuccess: (res: FnResult) => {
      toast(res.message, { tone: 'info' })
      invalidateSources()
      invalidateLinks()
    },
    onError: onFail,
  })

  // ── add catalog source modal ──────────────────────────────────────────
  const [form, setForm] = useState<CatForm | null>(null)
  const create = useMutation({
    mutationFn: (f: CatForm) => {
      const input: CatalogSourceInput = {
        name: f.name.trim(),
        type: f.type,
        path: f.path,
        description: f.description,
      }
      return insertCatalogSource(input)
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
      ) : loading ? (
        <div className="flex justify-center py-10">
          <Spinner size={18} />
        </div>
      ) : rows.length === 0 ? (
        <EmptyState
          title="No catalog sources"
          description="Add a schema catalog and link it to one or more data sources."
          action={
            <Button variant="primary" size="sm" onClick={() => setForm(emptyCatForm())}>
              ＋ Add catalog source
            </Button>
          }
        />
      ) : (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(330px,1fr))] gap-3">
          {rows.map((c) => {
            const linked = linksByCatalog.get(c.name) ?? []
            const available = allDsNames.filter((n) => !linked.includes(n))
            return (
              <div
                key={c.name}
                className="flex flex-col gap-2.5 rounded-card border border-border bg-surface p-4 shadow-card"
              >
                <div className="flex items-baseline gap-2">
                  <span className="font-mono text-sm font-bold">{c.name}</span>
                  <Badge tone="neutral">{c.type}</Badge>
                </div>
                {c.description && <div className="text-xs text-text2">{c.description}</div>}
                <div className="truncate font-mono text-2xs text-text3" title={c.path}>
                  {c.path}
                </div>

                <div className="flex flex-col gap-1.5">
                  <span className="eyebrow">Linked sources</span>
                  <div className="flex flex-wrap gap-1.5">
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
                          <div className="px-2 py-1.5 text-2xs text-text3">
                            All data sources linked
                          </div>
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
                </div>

                <div className="mt-1 flex items-center gap-2 border-t border-border pt-2.5">
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => reindex.mutate(c.name)}
                    title="_schema_reindex(name, batch_size)"
                  >
                    Reindex
                  </Button>
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => versionClean.mutate(c.name)}
                    title="_schema_version_clean(name)"
                  >
                    Version clean
                  </Button>
                  <span className="flex-1" />
                  <Menu>
                    <MenuTrigger asChild>
                      <Button size="icon" variant="ghost" aria-label="more actions">
                        ⋯
                      </Button>
                    </MenuTrigger>
                    <MenuContent>
                      <MenuItem danger onSelect={() => hardRemove.mutate(c.name)}>
                        Hard remove schema
                      </MenuItem>
                      <MenuSeparator />
                      <MenuItem danger onSelect={() => removeSource.mutate(c.name)}>
                        Delete catalog source
                      </MenuItem>
                    </MenuContent>
                  </Menu>
                </div>
              </div>
            )
          })}
        </div>
      )}

      <ApiHint>core.catalog_sources / core.catalogs</ApiHint>

      {/* ── add catalog source modal ── */}
      <Modal
        open={!!form}
        onOpenChange={(o) => !o && setForm(null)}
        title="Add catalog source"
        description="insert_catalog_sources(data:{…})"
        width={420}
        footer={
          form && (
            <>
              <Button variant="secondary" size="sm" onClick={() => setForm(null)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                size="sm"
                disabled={!form.name.trim() || create.isPending}
                onClick={() => create.mutate(form)}
              >
                Create
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
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder="mesh-core"
                />
              </Field>
              <Field label="type">
                <Select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })}>
                  <option value="localFS">localFS</option>
                  <option value="uri">uri</option>
                </Select>
              </Field>
            </div>
            <Field label="path">
              <Input
                mono
                value={form.path}
                onChange={(e) => setForm({ ...form, path: e.target.value })}
                placeholder="/schemas/core"
              />
            </Field>
            <Field label="description">
              <Input
                value={form.description}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
              />
            </Field>
          </div>
        )}
      </Modal>
    </Page>
  )
}
