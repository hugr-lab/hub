import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiHint } from '@/components/shell/Page'
import { cn } from '@/lib/cn'
import {
  Badge,
  Banner,
  Button,
  Collapsible,
  DataTable,
  Drawer,
  Field,
  Input,
  Spinner,
  Tabs,
  Textarea,
  Toggle,
  useToast,
  type Column,
  type Tone,
} from '@/components/ui'
import {
  checkAccess,
  deleteRolePermission,
  insertRolePermission,
  listRolePermissionCounts,
  listRolePermissions,
  listRoles,
  updateRolePermission,
  validateJson,
  validatePermissionFilter,
  type FieldVerdict,
  type PermissionKey,
  type Role,
  type RolePermission,
  type Verdict,
} from '@/api/platform-roles'

// ---------------------------------------------------------------------------
// Effective-access preview (a representative slice of the data schema)
// ---------------------------------------------------------------------------

type NodeKind = 'table' | 'view' | 'function'

const SCHEMA_OUTLINE: { module: string; type: string; kind: NodeKind; fields: string[] }[] = [
  { module: 'wh', type: 'wh.orders', kind: 'table', fields: ['id', 'owner_id', 'total', 'shipped_at'] },
  { module: 'bill', type: 'bill.invoices', kind: 'table', fields: ['id', 'owner_id', 'amount', 'status'] },
  { module: 'iot', type: 'iot.gateways', kind: 'table', fields: ['id', 'site', 'status', 'last_seen'] },
  { module: 'core', type: 'core.data_sources', kind: 'table', fields: ['name', 'type', 'path'] },
  { module: 'core', type: 'core.api_keys', kind: 'table', fields: ['name', 'key', 'default_role'] },
]

interface TypeAccess {
  type: string
  kind: NodeKind
  typeVerdict: FieldVerdict
  fields: FieldVerdict[]
}

interface EffectiveAccess {
  schema: TypeAccess[]
  adminAllowed: boolean
}

async function buildEffectiveAccess(role: string): Promise<EffectiveAccess> {
  const schema = await Promise.all(
    SCHEMA_OUTLINE.map(async (t) => {
      const verdicts = await checkAccess(role, t.type, ['*', ...t.fields])
      return { type: t.type, kind: t.kind, typeVerdict: verdicts[0], fields: verdicts.slice(1) }
    }),
  )
  const hub = await checkAccess(role, 'hub:management', ['admin'])
  return { schema, adminAllowed: hub[0]?.verdict === 'allow' }
}

// ---------------------------------------------------------------------------
// Small presentational helpers
// ---------------------------------------------------------------------------

const VERDICT_TONE: Record<Verdict, Tone> = {
  allow: 'green',
  hidden: 'neutral',
  deny: 'red',
  filtered: 'amber',
}

const KIND_SYMBOL: Record<NodeKind, string> = { table: '≡', view: '≣', function: 'ƒ' }
const gqlName = (t: string) => t.replace(/\./g, '_')

function VerdictBadge({ v }: { v: FieldVerdict }) {
  return (
    <Badge tone={VERDICT_TONE[v.verdict]} title={v.reason}>
      {v.verdict}
    </Badge>
  )
}

function KindChip({ kind }: { kind: NodeKind }) {
  return (
    <span className="flex-none rounded-chip bg-surface2 px-1.5 font-mono text-2xs font-bold text-text2">
      {KIND_SYMBOL[kind]}
    </span>
  )
}

// ---------------------------------------------------------------------------
// Rule validation
// ---------------------------------------------------------------------------

interface RuleError {
  type?: string
  field?: string
  filter?: string
  data?: string
}

function ruleErrorsOf(r: RolePermission): RuleError {
  const v: RuleError = {}
  const partial = (x: string) => x.includes('*') && x !== '*'
  if (!r.type_name.trim()) v.type = 'Type is required'
  else if (partial(r.type_name)) v.type = 'Partial wildcards are not supported — use * or an exact name'
  if (!r.field_name.trim()) v.field = 'Field is required'
  else if (partial(r.field_name)) v.field = 'Partial wildcards are not supported — use * or an exact name'
  const fe = validatePermissionFilter(r.filter)
  if (fe) v.filter = fe
  const de = validateJson(r.data)
  if (de) v.data = de
  return v
}

const pkOf = (role: string, r: RolePermission): PermissionKey => ({
  role,
  type_name: r.type_name,
  field_name: r.field_name,
})

// ---------------------------------------------------------------------------
// Screen
// ---------------------------------------------------------------------------

interface UpdateVars {
  orig: PermissionKey
  next: RolePermission
  label: string
  cid?: string
}

interface FilterDraft {
  row: RolePermission
  filter: string
  data: string
}

const AUTH_CLAIMS = [
  '[$auth.user_id]',
  '[$auth.user_name]',
  '[$auth.role]',
  '[$auth.auth_type]',
  '[$auth.provider]',
]

export function RolesScreen() {
  const qc = useQueryClient()
  const toast = useToast()

  const roles = useQuery({ queryKey: ['roles'], queryFn: listRoles })
  const counts = useQuery({ queryKey: ['rolePermissionCounts'], queryFn: listRolePermissionCounts })

  const [sel, setSel] = useState<string | null>(null)
  const preferred = counts.data
    ? roles.data?.find((r) => (counts.data![r.name] ?? 0) > 0)?.name
    : undefined
  const activeRole = sel ?? preferred ?? roles.data?.[0]?.name ?? ''
  const activeRoleObj: Role | undefined = roles.data?.find((r) => r.name === activeRole)

  const perms = useQuery({
    queryKey: ['rolePermissions', activeRole],
    queryFn: () => listRolePermissions(activeRole),
    enabled: !!activeRole,
  })
  const effective = useQuery({
    queryKey: ['effectiveAccess', activeRole],
    queryFn: () => buildEffectiveAccess(activeRole),
    enabled: !!activeRole,
  })

  const [effTab, setEffTab] = useState<'schema' | 'hub'>('schema')
  const [typeEdits, setTypeEdits] = useState<Record<string, string>>({})
  const [fieldEdits, setFieldEdits] = useState<Record<string, string>>({})
  const [fb, setFb] = useState<FilterDraft | null>(null)

  const invalidatePerms = () => {
    qc.invalidateQueries({ queryKey: ['rolePermissions', activeRole] })
    qc.invalidateQueries({ queryKey: ['effectiveAccess', activeRole] })
    qc.invalidateQueries({ queryKey: ['rolePermissionCounts'] })
  }
  const clearBuffer = (cid: string) => {
    setTypeEdits((e) => {
      const n = { ...e }
      delete n[cid]
      return n
    })
    setFieldEdits((e) => {
      const n = { ...e }
      delete n[cid]
      return n
    })
  }

  const insertMut = useMutation({
    mutationFn: (perm: RolePermission) => insertRolePermission(perm),
    onSuccess: () => {
      invalidatePerms()
      toast.success(`insert_role_permissions(role:"${activeRole}") → draft rule`)
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : 'insert_role_permissions failed'),
  })
  const updateMut = useMutation({
    mutationFn: (v: UpdateVars) => updateRolePermission(v.orig, v.next),
    onSuccess: (_r, v) => {
      invalidatePerms()
      if (v.cid) clearBuffer(v.cid)
      toast.success(v.label)
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : 'update_role_permissions failed'),
  })
  const deleteMut = useMutation({
    mutationFn: (key: PermissionKey) => deleteRolePermission(key),
    onSuccess: (_r, key) => {
      invalidatePerms()
      toast.success(`delete_role_permissions(role:"${key.role}", type_name:"${key.type_name}")`)
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : 'delete_role_permissions failed'),
  })

  const rules = perms.data ?? []

  // aggregate validation errors for the red banner
  const bannerErrors: string[] = []
  rules.forEach((r, i) => {
    const effRule = effectiveRule(r, cidOf(r, i))
    const v = ruleErrorsOf(effRule)
    const label = `Rule ${i + 1} (${effRule.type_name || '—'} . ${effRule.field_name || '—'}): `
    ;(['type', 'field', 'filter', 'data'] as const).forEach((k) => {
      if (v[k]) bannerErrors.push(label + v[k])
    })
  })

  function cidOf(r: RolePermission, i: number) {
    return `${i}::${r.type_name}::${r.field_name}`
  }
  function effectiveRule(r: RolePermission, cid: string): RolePermission {
    return {
      ...r,
      type_name: typeEdits[cid] ?? r.type_name,
      field_name: fieldEdits[cid] ?? r.field_name,
    }
  }

  const commitIdentity = (r: RolePermission, cid: string) => {
    const nt = typeEdits[cid] ?? r.type_name
    const nf = fieldEdits[cid] ?? r.field_name
    if (nt === r.type_name && nf === r.field_name) return
    updateMut.mutate({
      orig: pkOf(activeRole, r),
      next: { ...r, type_name: nt, field_name: nf },
      label: `update_role_permissions(role:"${activeRole}", type_name:"${nt}")`,
      cid,
    })
  }
  const commitToggle = (r: RolePermission, patch: Partial<RolePermission>, label: string) => {
    updateMut.mutate({ orig: pkOf(activeRole, r), next: { ...r, ...patch }, label })
  }

  const applyFilter = (filter: string, data: string) => {
    if (!fb) return
    updateMut.mutate({
      orig: pkOf(activeRole, fb.row),
      next: { ...fb.row, filter: filter.trim(), data: data.trim() },
      label: `update_role_permissions(role:"${activeRole}") → filter saved`,
    })
    setFb(null)
  }

  const addRule = () =>
    insertMut.mutate({
      role: activeRole,
      type_name: '*',
      field_name: '*',
      hidden: false,
      disabled: false,
      filter: '',
      data: '',
    })

  // ---- rules table columns ----
  const columns: Column<RolePermission>[] = [
    {
      key: 'type',
      header: 'Type',
      width: 'minmax(110px,1.1fr)',
      cell: (r, i) => {
        const cid = cidOf(r, i)
        const err = ruleErrorsOf(effectiveRule(r, cid)).type
        return (
          <Input
            mono
            className={cn('h-7 py-1 text-xs', err && 'border-red')}
            title={err ?? undefined}
            value={typeEdits[cid] ?? r.type_name}
            onChange={(e) => setTypeEdits((s) => ({ ...s, [cid]: e.target.value }))}
            onBlur={() => commitIdentity(r, cid)}
          />
        )
      },
    },
    {
      key: 'field',
      header: 'Field',
      width: 'minmax(70px,0.8fr)',
      cell: (r, i) => {
        const cid = cidOf(r, i)
        const err = ruleErrorsOf(effectiveRule(r, cid)).field
        return (
          <Input
            mono
            className={cn('h-7 py-1 text-xs', err && 'border-red')}
            title={err ?? undefined}
            value={fieldEdits[cid] ?? r.field_name}
            onChange={(e) => setFieldEdits((s) => ({ ...s, [cid]: e.target.value }))}
            onBlur={() => commitIdentity(r, cid)}
          />
        )
      },
    },
    {
      key: 'hidden',
      header: 'Hidden',
      width: '64px',
      align: 'center',
      cell: (r) => (
        <Toggle
          checked={r.hidden}
          onCheckedChange={(v) =>
            commitToggle(r, { hidden: v }, `update_role_permissions("${r.type_name}", hidden: ${v})`)
          }
        />
      ),
    },
    {
      key: 'deny',
      header: 'Deny',
      width: '64px',
      align: 'center',
      cell: (r) => (
        <Toggle
          checked={r.disabled}
          onCheckedChange={(v) =>
            commitToggle(r, { disabled: v }, `update_role_permissions("${r.type_name}", disabled: ${v})`)
          }
        />
      ),
    },
    {
      key: 'filter',
      header: 'Row filter / defaults',
      width: 'minmax(140px,1.4fr)',
      cell: (r) => {
        const summary = r.filter ? r.filter : r.data ? `data: ${r.data}` : '＋ filter / defaults'
        return (
          <button
            title="Open filter builder"
            onClick={() => setFb({ row: r, filter: r.filter, data: r.data })}
            className={cn(
              'w-full truncate rounded-btn border border-dashed border-border2 bg-surface2 px-2 py-1 text-left font-mono text-xs hover:border-accent',
              r.filter || r.data ? 'text-text' : 'text-text3',
            )}
          >
            {summary}
          </button>
        )
      },
    },
    {
      key: 'del',
      header: '',
      width: '40px',
      align: 'center',
      cell: (r) => (
        <button
          title="Delete rule"
          className="text-xs font-semibold text-red hover:underline"
          onClick={() => deleteMut.mutate(pkOf(activeRole, r))}
        >
          Del
        </button>
      ),
    },
  ]

  const hubRows = [
    { label: 'Console admin sections (Platform, Skills grants)', ok: !!effective.data?.adminAllowed, note: 'hub:management.admin' },
    { label: 'Chat with granted agents', ok: true, note: 'user_agents grants' },
    { label: 'Agent lifecycle (start / stop)', ok: !!effective.data?.adminAllowed, note: 'admin only' },
    { label: 'Publish skills to the catalog', ok: !!effective.data?.adminAllowed, note: 'set_skill_publish' },
    { label: 'Impersonation', ok: !!activeRoleObj?.can_impersonate, note: 'can_impersonate' },
  ]

  return (
    <div className="flex min-h-0 flex-1 flex-row">
      {/* left rail — core.roles */}
      <aside className="flex w-[250px] flex-none flex-col gap-0.5 overflow-y-auto border-r border-border bg-surface p-2">
        <div className="eyebrow px-2.5 pb-1.5 pt-0.5">core.roles</div>
        {roles.isLoading && <div className="px-2.5 py-2 text-xs text-text3">Loading roles…</div>}
        {roles.data?.map((r) => {
          const active = r.name === activeRole
          const n = counts.data?.[r.name] ?? 0
          return (
            <button
              key={r.name}
              onClick={() => setSel(r.name)}
              className={cn(
                'flex flex-col gap-0.5 rounded-[7px] px-2.5 py-1.5 text-left transition-colors',
                active ? 'bg-accent-soft' : 'hover:bg-surface2',
              )}
            >
              <span className="flex w-full items-center gap-1.5">
                <span
                  className={cn(
                    'flex-1 truncate font-mono text-xs font-semibold',
                    active ? 'text-accent' : 'text-text',
                  )}
                >
                  {r.name}
                </span>
                <span className="text-2xs font-semibold text-text3">
                  {n ? `${n} rule${n === 1 ? '' : 's'}` : 'no rules'}
                </span>
                {r.disabled && <span className="text-2xs font-bold uppercase text-red">off</span>}
              </span>
              <span className="truncate text-2xs text-text3">{r.description}</span>
            </button>
          )
        })}
      </aside>

      {/* right — rule editor + effective access */}
      <section className="flex min-w-0 flex-1 flex-col gap-3.5 overflow-y-auto px-[22px] py-5">
        <div className="flex items-center gap-3">
          <div className="min-w-0">
            <div className="truncate font-mono text-[15px] font-bold">{activeRole || '—'}</div>
            <div className="truncate text-xs text-text3">{activeRoleObj?.description}</div>
          </div>
          <span className="flex-1" />
          <Button variant="primary" size="sm" disabled={!activeRole || insertMut.isPending} onClick={addRule}>
            ＋ Add rule
          </Button>
        </div>

        <Banner tone="info" className="flex items-start gap-2">
          <span className="font-bold text-accent">ⓘ</span>
          <span>
            <b>Access is ALLOW by default.</b> A rule only takes effect when it <b>hides</b>,{' '}
            <b>disables</b>, or <b>row-filters</b> a (type, field).
          </span>
        </Banner>

        {bannerErrors.length > 0 && (
          <Banner tone="error" className="flex flex-col gap-1">
            {bannerErrors.map((msg, i) => (
              <span key={i} className="flex items-baseline gap-2">
                <span className="font-bold text-red">✕</span>
                <span>{msg}</span>
              </span>
            ))}
          </Banner>
        )}

        {rules.length > 0 ? (
          <DataTable columns={columns} rows={rules} getKey={(r, i) => cidOf(r, i)} />
        ) : (
          <div className="rounded-card border border-dashed border-border2 px-6 py-7 text-center text-sm text-text3">
            {perms.isLoading
              ? 'Loading rules…'
              : 'No restriction rules — this role sees everything. Add a rule to hide, deny or row-filter access.'}
          </div>
        )}

        {/* effective access */}
        <div className="overflow-hidden rounded-card border border-border bg-surface">
          <div className="flex items-center gap-3 px-4 pb-1 pt-2.5">
            <span className="text-sm font-semibold">Effective access</span>
            <span className="flex-1" />
            <span className="font-mono text-2xs text-text3">check_access</span>
          </div>
          <Tabs<'schema' | 'hub'>
            className="px-4"
            tabs={[
              { value: 'schema', label: 'Data schema' },
              { value: 'hub', label: 'Agent & console' },
            ]}
            value={effTab}
            onChange={setEffTab}
          />

          {effTab === 'schema' ? (
            <div className="max-h-80 overflow-y-auto p-2">
              {effective.isLoading ? (
                <div className="flex items-center gap-2 px-2 py-3 text-xs text-text3">
                  <Spinner /> Evaluating check_access…
                </div>
              ) : (
                effective.data?.schema.map((t) => (
                  <Collapsible
                    key={t.type}
                    className="rounded-btn px-1 hover:bg-surface2"
                    headerClassName="py-1.5"
                    header={
                      <span className="flex items-center gap-2">
                        <KindChip kind={t.kind} />
                        <span className="truncate font-mono text-xs">{t.type}</span>
                        <span className="truncate font-mono text-2xs text-text3">
                          [{gqlName(t.type)}]
                        </span>
                        <span className="flex-1" />
                        <VerdictBadge v={t.typeVerdict} />
                      </span>
                    }
                  >
                    <div className="flex flex-col gap-0.5 pb-1.5 pl-7 pr-1">
                      {t.fields.map((f) => (
                        <div key={f.field} className="flex items-center gap-2 py-0.5">
                          <span className="truncate font-mono text-2xs text-text2">{f.field}</span>
                          <span className="flex-1" />
                          <VerdictBadge v={f} />
                        </div>
                      ))}
                    </div>
                  </Collapsible>
                ))
              )}
            </div>
          ) : (
            <div className="flex flex-col px-4 py-1.5">
              {hubRows.map((h) => (
                <div key={h.label} className="flex items-center gap-2.5 border-b border-border py-2 text-sm last:border-b-0">
                  <span className={cn('w-3.5 font-bold', h.ok ? 'text-green' : 'text-text3')}>
                    {h.ok ? '✓' : '✕'}
                  </span>
                  <span className="flex-1 text-text">{h.label}</span>
                  <span className="font-mono text-2xs text-text3">{h.note}</span>
                </div>
              ))}
            </div>
          )}
        </div>

        <ApiHint>
          insert_role_permissions / update_role_permissions / delete_role_permissions · [$auth.user_id]
          [$auth.role] interpolate at query time
        </ApiHint>
      </section>

      {/* filter builder drawer */}
      <Drawer
        open={!!fb}
        onOpenChange={(o) => !o && setFb(null)}
        title="Row filter builder"
        subtitle={fb ? `${activeRole} · ${fb.row.type_name} . ${fb.row.field_name}` : undefined}
        width={440}
        footer={
          fb && (
            <>
              <Button
                variant="danger-ghost"
                size="sm"
                className="mr-auto"
                onClick={() => applyFilter('', '')}
              >
                Clear filter
              </Button>
              <Button variant="secondary" size="sm" onClick={() => setFb(null)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                size="sm"
                disabled={
                  !!validatePermissionFilter(fb.filter) || !!validateJson(fb.data)
                }
                onClick={() => applyFilter(fb.filter, fb.data)}
              >
                Apply
              </Button>
            </>
          )
        }
      >
        {fb && <FilterBuilder draft={fb} onChange={setFb} />}
      </Drawer>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Filter builder body
// ---------------------------------------------------------------------------

function FilterBuilder({
  draft,
  onChange,
}: {
  draft: FilterDraft
  onChange: (d: FilterDraft) => void
}) {
  const filterErr = validatePermissionFilter(draft.filter)
  const dataErr = validateJson(draft.data)
  return (
    <div className="flex flex-col gap-3.5">
      <p className="text-xs text-text2">
        The filter is a hugr filter expression in JSON — it can nest related fields and combine{' '}
        <span className="font-mono">_and</span> / <span className="font-mono">_or</span> /{' '}
        <span className="font-mono">_not</span>, including list conditions like{' '}
        <span className="font-mono">any_of</span>.
      </p>

      <Field
        label="Filter"
        hint={
          filterErr ??
          'Operators: eq gt gte lt lte like ilike in is_null · nested any_of / all_of on lists · no neq, wrap in _not.'
        }
      >
        <Textarea
          mono
          rows={11}
          spellCheck={false}
          className={cn('bg-surface2 text-xs', filterErr && 'border-red')}
          placeholder={
            '{\n  "_or": [\n    { "items": { "any_of": { "product_id": { "in": [1, 233] } } } },\n    { "shipped_at": { "is_null": false } }\n  ],\n  "owner_id": { "eq": "[$auth.user_id]" }\n}'
          }
          value={draft.filter}
          onChange={(e) => onChange({ ...draft, filter: e.target.value })}
        />
      </Field>

      <div className="flex flex-col gap-1.5">
        <span className="text-xs font-medium text-text2">Auth claim placeholders</span>
        <div className="flex flex-wrap gap-1.5">
          {AUTH_CLAIMS.map((c) => (
            <button
              key={c}
              onClick={() => onChange({ ...draft, filter: draft.filter + `"${c}"` })}
              className="rounded-btn border border-border2 bg-surface2 px-2 py-0.5 font-mono text-2xs font-semibold text-accent hover:border-accent"
            >
              {c}
            </button>
          ))}
        </div>
        <span className="text-2xs text-text3">
          Click to append into the filter — interpolated from the caller's token at query time.
        </span>
      </div>

      <Field label="Data (insert-stamp)" hint={dataErr ?? undefined}>
        <Textarea
          mono
          rows={3}
          spellCheck={false}
          className={cn('bg-surface2 text-xs', dataErr && 'border-red')}
          placeholder={'{ "owner_id": "[$auth.user_id]" }'}
          value={draft.data}
          onChange={(e) => onChange({ ...draft, data: e.target.value })}
        />
      </Field>
    </div>
  )
}
