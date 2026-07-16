import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** `core.roles` row. */
export interface Role {
  name: string
  description: string
  disabled: boolean
  can_impersonate: boolean
}

/**
 * `core.role_permissions` row. The model is ALLOW-by-default: a row only takes
 * effect when it hides, disables, or row-filters a `(type_name, field_name)`.
 * `filter` (RLS) and `data` (insert-stamp) are JSON columns; we carry them as
 * raw JSON text in the UI (`''` = none) and parse on the way to the backend.
 */
export interface RolePermission {
  role: string
  type_name: string
  field_name: string
  hidden: boolean
  disabled: boolean
  filter: string
  data: string
}

/** Composite primary key of a `core.role_permissions` row. */
export interface PermissionKey {
  role: string
  type_name: string
  field_name: string
}

export type Verdict = 'allow' | 'hidden' | 'deny' | 'filtered'

/** One effective-access verdict for a `(type_name, field)` under a role. */
export interface FieldVerdict {
  /** `'*'` denotes the object/type-level verdict. */
  field: string
  verdict: Verdict
  reason: string
}

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

const NEQ_OPERATOR = /"neq"\s*:|[{,\s]neq\s*:/

/**
 * Validate a permission `filter` JSON expression.
 *
 * hugr filters have NO `neq` — negation must be written as
 * `_not:{ field:{ eq: … } }`. Returns a human-readable error message when the
 * text is not valid JSON or uses `neq`, or `null` when the filter is OK
 * (an empty string is OK — it means "no row filter").
 */
export function validatePermissionFilter(json: string): string | null {
  const trimmed = json.trim()
  if (!trimmed) return null
  if (NEQ_OPERATOR.test(trimmed)) {
    return 'neq is not supported — negate with _not:{ field:{ eq: … } }'
  }
  try {
    JSON.parse(trimmed)
  } catch {
    return 'Filter is not valid JSON'
  }
  return null
}

/** Validate arbitrary JSON text (used for the `data` insert-stamp). */
export function validateJson(json: string): string | null {
  const trimmed = json.trim()
  if (!trimmed) return null
  try {
    JSON.parse(trimmed)
    return null
  } catch {
    return 'Not valid JSON'
  }
}

/**
 * Effective-access verdict for one `(type_name, field)` under a role's rules —
 * ALLOW-by-default with most-specific rule winning. Mirrors the server's
 * `check_access` semantics and drives the offline demo preview.
 */
export function computeVerdict(
  rules: RolePermission[],
  typeName: string,
  field: string,
): FieldVerdict {
  const wildcardField = field === '*'
  const cand = rules.filter(
    (r) =>
      (r.type_name === '*' || r.type_name === typeName) &&
      (r.field_name === '*' || wildcardField || r.field_name === field),
  )
  if (cand.length === 0) {
    return { field, verdict: 'allow', reason: 'no rule — ALLOW by default' }
  }
  const score = (r: RolePermission) =>
    (r.type_name === '*' ? 0 : 2) + (r.field_name === '*' ? 0 : 1)
  cand.sort((a, b) => score(b) - score(a))
  const r = cand[0]
  if (r.hidden) return { field, verdict: 'hidden', reason: `hidden by rule on ${r.type_name}` }
  if (r.disabled) return { field, verdict: 'deny', reason: `disabled by rule on ${r.type_name}` }
  if (r.filter) return { field, verdict: 'filtered', reason: 'row-level filter applies' }
  return { field, verdict: 'allow', reason: 'explicit allow overrides broader rule' }
}

// ---------------------------------------------------------------------------
// Demo store (mutable so the offline `?demo=1` screen stays interactive)
// ---------------------------------------------------------------------------

const MOCK_ROLES: Role[] = [
  { name: 'admin', description: 'Full platform management', disabled: false, can_impersonate: true },
  { name: 'analyst', description: 'Query access, own-row billing', disabled: false, can_impersonate: false },
  {
    name: 'agent:analytics',
    description: 'Floored role for analytics-copilot',
    disabled: false,
    can_impersonate: false,
  },
  { name: 'viewer', description: 'Read-only catalog browsing', disabled: false, can_impersonate: false },
  { name: 'service-etl', description: 'API-key role for pipelines', disabled: true, can_impersonate: false },
]

const MOCK_PERMS: Record<string, RolePermission[]> = {
  admin: [],
  analyst: [
    { role: 'analyst', type_name: 'hub:management', field_name: 'admin', hidden: false, disabled: true, filter: '', data: '' },
    { role: 'analyst', type_name: 'core.api_keys', field_name: '*', hidden: true, disabled: false, filter: '', data: '' },
    {
      role: 'analyst',
      type_name: 'bill.invoices',
      field_name: '*',
      hidden: false,
      disabled: false,
      filter: '{ "owner_id": { "eq": "[$auth.user_id]" } }',
      data: '{ "owner_id": "[$auth.user_id]" }',
    },
  ],
  'agent:analytics': [
    { role: 'agent:analytics', type_name: '*', field_name: '*', hidden: false, disabled: true, filter: '', data: '' },
    { role: 'agent:analytics', type_name: 'wh.orders', field_name: '*', hidden: false, disabled: false, filter: '', data: '' },
    { role: 'agent:analytics', type_name: 'iot.gateways', field_name: '*', hidden: false, disabled: false, filter: '', data: '' },
  ],
  viewer: [
    { role: 'viewer', type_name: '*', field_name: '*', hidden: false, disabled: true, filter: '', data: '' },
    { role: 'viewer', type_name: 'core.data_sources', field_name: '*', hidden: false, disabled: false, filter: '', data: '' },
  ],
  'service-etl': [],
}

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T
const samePK = (a: PermissionKey, b: PermissionKey) =>
  a.role === b.role && a.type_name === b.type_name && a.field_name === b.field_name

// ---------------------------------------------------------------------------
// JSON-scalar helpers (backend returns `filter`/`data` as JSON values)
// ---------------------------------------------------------------------------

interface RawPermission {
  role: string
  type_name: string
  field_name: string
  hidden: boolean
  disabled: boolean
  filter: unknown
  data: unknown
}

const jsonToText = (v: unknown): string =>
  v == null ? '' : typeof v === 'string' ? v : JSON.stringify(v, null, 2)

const textToJson = (text: string): unknown => {
  const trimmed = text.trim()
  if (!trimmed) return null
  try {
    return JSON.parse(trimmed)
  } catch {
    return null
  }
}

const rawToPermission = (r: RawPermission): RolePermission => ({
  role: r.role,
  type_name: r.type_name,
  field_name: r.field_name,
  hidden: r.hidden,
  disabled: r.disabled,
  filter: jsonToText(r.filter),
  data: jsonToText(r.data),
})

const permissionToInput = (p: RolePermission) => ({
  role: p.role,
  type_name: p.type_name,
  field_name: p.field_name,
  hidden: p.hidden,
  disabled: p.disabled,
  filter: textToJson(p.filter),
  data: textToJson(p.data),
})

// hugr-generated input type names (verified against the live schema): insert →
// `core_<table>_mut_input_data`, update → `core_<table>_mut_data`, filter →
// `core_<table>_filter` (NOT `_filter_input`).
// ---------------------------------------------------------------------------
// Roles
// ---------------------------------------------------------------------------

export async function listRoles(): Promise<Role[]> {
  return withDemo(
    () => clone(MOCK_ROLES).sort((a, b) => a.name.localeCompare(b.name)),
    async () => {
      const d = await postGraphQL<{ core: { roles: Role[] } }>(
        `query { core { roles { name description disabled can_impersonate } } }`,
      )
      return [...d.core.roles].sort((a, b) => a.name.localeCompare(b.name))
    },
  )
}

export async function insertRole(role: Role): Promise<Role> {
  return withDemo(
    () => {
      MOCK_ROLES.push(clone(role))
      MOCK_PERMS[role.name] = MOCK_PERMS[role.name] ?? []
      return role
    },
    async () => {
      await postGraphQL(
        `mutation($data: core_roles_mut_input_data!){ core { insert_roles(data: $data){ name } } }`,
        { data: role },
      )
      return role
    },
  )
}

export async function updateRole(name: string, patch: Partial<Omit<Role, 'name'>>): Promise<void> {
  return withDemo(
    () => {
      const i = MOCK_ROLES.findIndex((r) => r.name === name)
      if (i >= 0) MOCK_ROLES[i] = { ...MOCK_ROLES[i], ...patch }
    },
    async () => {
      await postGraphQL(
        `mutation($filter: core_roles_filter!, $data: core_roles_mut_data!){ core { update_roles(filter: $filter, data: $data){ success message } } }`,
        { filter: { name: { eq: name } }, data: patch },
      )
    },
  )
}

export async function deleteRole(name: string): Promise<void> {
  return withDemo(
    () => {
      const i = MOCK_ROLES.findIndex((r) => r.name === name)
      if (i >= 0) MOCK_ROLES.splice(i, 1)
      delete MOCK_PERMS[name]
    },
    async () => {
      await postGraphQL(
        `mutation($filter: core_roles_filter!){ core { delete_roles(filter: $filter){ success message } } }`,
        { filter: { name: { eq: name } } },
      )
    },
  )
}

// ---------------------------------------------------------------------------
// Role permissions
// ---------------------------------------------------------------------------

export async function listRolePermissions(role: string): Promise<RolePermission[]> {
  return withDemo(
    () => clone(MOCK_PERMS[role] ?? []),
    async () => {
      const d = await postGraphQL<{ core: { role_permissions: RawPermission[] } }>(
        `query($role: String!){ core { role_permissions(filter: { role: { eq: $role } }) { role type_name field_name hidden disabled filter data } } }`,
        { role },
      )
      return d.core.role_permissions.map(rawToPermission)
    },
  )
}

/** Rule count per role (drives the "N rules" / "no rules" left-rail label). */
export async function listRolePermissionCounts(): Promise<Record<string, number>> {
  return withDemo(
    () => {
      const out: Record<string, number> = {}
      for (const [role, rows] of Object.entries(MOCK_PERMS)) out[role] = rows.length
      return out
    },
    async () => {
      const d = await postGraphQL<{ core: { role_permissions: { role: string }[] } }>(
        `query { core { role_permissions { role } } }`,
      )
      const out: Record<string, number> = {}
      for (const r of d.core.role_permissions) out[r.role] = (out[r.role] ?? 0) + 1
      return out
    },
  )
}

export async function insertRolePermission(perm: RolePermission): Promise<void> {
  return withDemo(
    () => {
      MOCK_PERMS[perm.role] = MOCK_PERMS[perm.role] ?? []
      MOCK_PERMS[perm.role].push(clone(perm))
    },
    async () => {
      await postGraphQL(
        `mutation($data: core_role_permissions_mut_input_data!){ core { insert_role_permissions(data: $data){ role } } }`,
        { data: permissionToInput(perm) },
      )
    },
  )
}

/**
 * Persist an edited rule. When the composite PK is unchanged this is a plain
 * update; when the type/field was renamed we delete the old key and insert the
 * new row (PK columns may be immutable server-side).
 */
export async function updateRolePermission(orig: PermissionKey, next: RolePermission): Promise<void> {
  const pkChanged = !samePK(orig, next)
  return withDemo(
    () => {
      const rows = (MOCK_PERMS[orig.role] = MOCK_PERMS[orig.role] ?? [])
      const i = rows.findIndex((r) => samePK(r, orig))
      if (i >= 0) rows[i] = clone(next)
      else rows.push(clone(next))
    },
    async () => {
      if (pkChanged) {
        await postGraphQL(
          `mutation($filter: core_role_permissions_filter!){ core { delete_role_permissions(filter: $filter){ success message } } }`,
          { filter: keyFilter(orig) },
        )
        await postGraphQL(
          `mutation($data: core_role_permissions_mut_input_data!){ core { insert_role_permissions(data: $data){ role } } }`,
          { data: permissionToInput(next) },
        )
        return
      }
      await postGraphQL(
        `mutation($filter: core_role_permissions_filter!, $data: core_role_permissions_mut_data!){ core { update_role_permissions(filter: $filter, data: $data){ success message } } }`,
        {
          filter: keyFilter(orig),
          data: {
            hidden: next.hidden,
            disabled: next.disabled,
            filter: textToJson(next.filter),
            data: textToJson(next.data),
          },
        },
      )
    },
  )
}

export async function deleteRolePermission(key: PermissionKey): Promise<void> {
  return withDemo(
    () => {
      const rows = MOCK_PERMS[key.role]
      if (!rows) return
      const i = rows.findIndex((r) => samePK(r, key))
      if (i >= 0) rows.splice(i, 1)
    },
    async () => {
      await postGraphQL(
        `mutation($filter: core_role_permissions_filter!){ core { delete_role_permissions(filter: $filter){ success message } } }`,
        { filter: keyFilter(key) },
      )
    },
  )
}

const keyFilter = (k: PermissionKey) => ({
  role: { eq: k.role },
  type_name: { eq: k.type_name },
  field_name: { eq: k.field_name },
})

// ---------------------------------------------------------------------------
// Effective access — `function.core.auth.check_access(type_name, fields)`
// ---------------------------------------------------------------------------

interface RawVerdict {
  field: string
  enabled: boolean
  visible: boolean
}

/** Map a check_access entry (visible/enabled flags) to a UI verdict. */
const verdictFrom = (v: RawVerdict): Verdict => {
  if (!v.visible) return 'hidden'
  if (!v.enabled) return 'deny'
  return 'allow'
}

/**
 * Effective-access verdicts for `fields` of `typeName`.
 *
 * The server function is `check_access(type_name: String!, fields: String!)`
 * where `fields` is a COMMA-SEPARATED list, returning
 * `core_auth_auth_access_check_entry { field, enabled, visible }`. It evaluates
 * for the CALLER — check_access has no role argument, so it cannot preview an
 * arbitrary role's access (the `role` arg only keys the query cache + drives the
 * offline demo, which computes verdicts from the mock rules). It also can't
 * report a 'filtered' verdict (no filter flag on the entry). See B-followup.
 */
export async function checkAccess(
  role: string,
  typeName: string,
  fields: string[],
): Promise<FieldVerdict[]> {
  return withDemo(
    () => fields.map((f) => computeVerdict(MOCK_PERMS[role] ?? [], typeName, f)),
    async () => {
      const d = await postGraphQL<{
        function: { core: { auth: { check_access: RawVerdict[] } } }
      }>(
        `query($t: String!, $f: String!){ function { core { auth { check_access(type_name: $t, fields: $f) { field enabled visible } } } } }`,
        { t: typeName, f: fields.join(',') },
      )
      const byField = new Map((d.function.core.auth.check_access ?? []).map((v) => [v.field, v]))
      return fields.map((f) => {
        const raw = byField.get(f)
        if (!raw) return { field: f, verdict: 'allow' as Verdict, reason: 'no rule — ALLOW by default' }
        return {
          field: f,
          verdict: verdictFrom(raw),
          reason: `check_access → visible=${raw.visible} enabled=${raw.enabled}`,
        }
      })
    },
  )
}
