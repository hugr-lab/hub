import { postGraphQL } from '@/lib/graphql'
import { withDemo, isDemoMode } from '@/lib/demo'

/**
 * Schema Explorer data layer — two lazily-expanded trees, both scoped to the
 * calling user (the OIDC bearer is forwarded, so role visibility applies).
 *
 * - **logical** — hugr's logical data model via the `_catalog` meta-queries
 *   (`_catalog` → `_module` → `_dataObject` / `_function`). The default view.
 * - **graphql** — the generated GraphQL schema via standard introspection
 *   (`__schema` roots → `__type`), enriched with hugr extensions
 *   (`hugr_type` / `catalog`).
 *
 * Every fetcher takes an optional `role`: when set it sends
 * `X-Hugr-Impersonated-Role`, so the very same trees can later back the
 * role-access preview on the permissions screen. Undefined = the caller's own
 * view.
 */

export type SchemaTree = 'logical' | 'graphql'

export type NodeKind =
  | 'root'
  | 'module'
  | 'object'
  | 'view'
  | 'function'
  | 'field'
  | 'arg'
  | 'inputField'
  | 'enumValue'
  | 'relation'
  | 'group'

export type BadgeTone = 'neutral' | 'green' | 'amber' | 'red' | 'blue' | 'accent'

export interface NodeBadge {
  text: string
  tone: BadgeTone
}

/** How to fetch a node's children (undefined → leaf). */
export type LoadSpec =
  | { t: 'gqlType'; name: string }
  | { t: 'gqlField'; returnName: string; returnKind: string; args: RawArg[]; fieldId: string }
  | { t: 'static'; children: SchemaNode[] }
  | { t: 'logicalModule'; name: string }
  | { t: 'logicalObjectExpand'; name: string }
  | { t: 'logicalTypes'; scope: 'SYSTEM' | 'SOURCE' }

/** How to render the detail panel when a node is selected (undefined → none). */
export type DetailSpec =
  | { t: 'gqlType'; name: string }
  | { t: 'gqlField'; typeName: string; name: string; typeLabel: string }
  | { t: 'logicalObject'; name: string }
  | { t: 'logicalModule'; name: string }
  | { t: 'logicalFunction'; module: string; name: string }

export interface SchemaNode {
  id: string
  label: string
  kind: NodeKind
  /** muted type-ref rendered after the label. */
  typeLabel?: string
  badges?: NodeBadge[]
  hasDescription?: boolean
  expandable: boolean
  selectable: boolean
  load?: LoadSpec
  detail?: DetailSpec
}

interface RawArg {
  name: string
  description: string | null
  defaultValue: string | null
  type: IntroTypeRef
}

// ── detail panel ───────────────────────────────────────────────────────────

export interface DetailField {
  ordinal: number
  name: string
  type: string
  description: string
  /** muted sub-line, e.g. `select · 3 args`. */
  extra?: string
}

export interface DetailRelation {
  name: string
  target: string
  direction: 'out' | 'in'
  kind?: string
}

export type SaveKind = 'type' | 'field' | 'module' | 'catalog'

export interface NodeDetail {
  id: string
  name: string
  kind: NodeKind
  badges: NodeBadge[]
  meta: string
  description: string
  /** main fields (object fields / logical columns / a field's return-type fields). */
  fields: DetailField[]
  /** section label for `fields` (e.g. "Fields" / "Return fields"). */
  fieldsLabel?: string
  /** argument list (functions, parameterized views, field args). */
  args?: DetailField[]
  /** enum values (ENUM types). */
  enumValues?: { name: string; description: string }[]
  relations: DetailRelation[]
  primaryKey?: string[]
  dataSource?: string
  /** which `_schema_update_*_desc` mutation persists this description (null = read-only). */
  saveKind: SaveKind | null
  /** GraphQL type name a field save targets (`_schema_update_field_desc(type_name)`). */
  typeName?: string
}

export interface SaveDescriptionInput {
  kind: SaveKind
  target: { name: string; typeName?: string }
  description: string
  longDescription?: string
}

// ── helpers ──────────────────────────────────────────────────────────────

function roleHeaders(role?: string): Record<string, string> | undefined {
  return role ? { 'X-Hugr-Impersonated-Role': role } : undefined
}

interface IntroTypeRef {
  kind: string
  name: string | null
  ofType: IntroTypeRef | null
}

function unwrap(t: IntroTypeRef | null): IntroTypeRef | null {
  let cur = t
  while (cur && cur.ofType && (cur.kind === 'NON_NULL' || cur.kind === 'LIST')) cur = cur.ofType
  return cur
}

function renderTypeRef(t: IntroTypeRef | null): string {
  if (!t) return ''
  if (t.kind === 'NON_NULL') return `${renderTypeRef(t.ofType)}!`
  if (t.kind === 'LIST') return `[${renderTypeRef(t.ofType)}]`
  return t.name ?? '?'
}

const HUGR_TYPE_TONE: Record<string, BadgeTone> = {
  select: 'accent',
  select_one: 'accent',
  aggregate: 'blue',
  bucket_agg: 'blue',
  function: 'amber',
  function_call: 'amber',
  mutation: 'amber',
}

function hugrBadge(hugrType?: string): NodeBadge[] {
  if (!hugrType) return []
  return [{ text: hugrType, tone: HUGR_TYPE_TONE[hugrType] ?? 'neutral' }]
}

// ═══════════════════════════════════════════════════════════════════════════
// Roots
// ═══════════════════════════════════════════════════════════════════════════

export async function loadRoots(tree: SchemaTree, role?: string): Promise<SchemaNode[]> {
  return tree === 'logical' ? loadLogicalRoots(role) : loadGraphqlRoots(role)
}

export async function loadChildren(node: SchemaNode, role?: string): Promise<SchemaNode[]> {
  if (!node.load) return []
  switch (node.load.t) {
    case 'static':
      return node.load.children
    case 'gqlType':
      return gqlTypeChildren(node.id, node.load.name, role)
    case 'gqlField':
      return gqlFieldChildren(node.load, role)
    case 'logicalModule':
      return logicalModuleChildren(node.id, node.load.name, role)
    case 'logicalObjectExpand':
      return logicalObjectExpandChildren(node.id, node.load.name, role)
    case 'logicalTypes':
      return logicalTypesChildren(node.id, node.load.scope, role)
  }
}

/** Sort tree nodes alphabetically by label (case-insensitive). */
function sortNodes(nodes: SchemaNode[]): SchemaNode[] {
  return [...nodes].sort((a, b) => a.label.localeCompare(b.label, undefined, { sensitivity: 'base' }))
}

// ═══════════════════════════════════════════════════════════════════════════
// GraphQL tree — __schema / __type introspection
// ═══════════════════════════════════════════════════════════════════════════

const REF = `kind name ofType { kind name ofType { kind name ofType { kind name } } }`

async function loadGraphqlRoots(role?: string): Promise<SchemaNode[]> {
  return withDemo(
    () => demoGraphqlRoots(),
    async () => {
      const d = await postGraphQL<{
        __schema: {
          queryType: { name: string } | null
          mutationType: { name: string } | null
          subscriptionType: { name: string } | null
        }
      }>(
        `{ __schema { queryType { name } mutationType { name } subscriptionType { name } } }`,
        {},
        roleHeaders(role),
      )
      const roots: Array<[string, string | null | undefined]> = [
        ['Query', d.__schema.queryType?.name],
        ['Mutation', d.__schema.mutationType?.name],
        ['Subscription', d.__schema.subscriptionType?.name],
      ]
      return roots
        .filter(([, name]) => !!name)
        .map(([label, name]) => ({
          id: `g:${label}`,
          label,
          kind: 'root' as const,
          typeLabel: name as string,
          expandable: true,
          selectable: true,
          load: { t: 'gqlType' as const, name: name as string },
          detail: { t: 'gqlType' as const, name: name as string },
        }))
    },
  )
}

interface IntroField {
  name: string
  description: string | null
  hugr_type: string | null
  catalog: string | null
  type: IntroTypeRef
  args: RawArg[]
}

async function introspectType(name: string, role?: string): Promise<{
  kind: string
  description: string | null
  hugr_type: string | null
  catalog: string | null
  module: string | null
  fields: IntroField[] | null
  inputFields: RawArg[] | null
  enumValues: { name: string; description: string | null }[] | null
}> {
  const d = await postGraphQL<{ __type: any }>(
    `query ($name: String!) {
      __type(name: $name) {
        kind description hugr_type catalog module
        fields { name description hugr_type catalog type { ${REF} } args { name description defaultValue type { ${REF} } } }
        inputFields { name description defaultValue type { ${REF} } }
        enumValues { name description }
      }
    }`,
    { name },
    roleHeaders(role),
  )
  const t = d.__type ?? {}
  return {
    kind: t.kind ?? 'OBJECT',
    description: t.description ?? null,
    hugr_type: t.hugr_type ?? null,
    catalog: t.catalog ?? null,
    module: t.module ?? null,
    fields: t.fields ?? null,
    inputFields: t.inputFields ?? null,
    enumValues: t.enumValues ?? null,
  }
}

async function gqlTypeChildren(parentId: string, typeName: string, role?: string): Promise<SchemaNode[]> {
  const t = await introspectType(typeName, role)
  if (t.kind === 'ENUM') {
    return sortNodes(
      (t.enumValues ?? []).map((ev) => ({
        id: `${parentId}>${ev.name}`,
        label: ev.name,
        kind: 'enumValue' as const,
        expandable: false,
        selectable: false,
      })),
    )
  }
  if (t.kind === 'INPUT_OBJECT') {
    return sortNodes((t.inputFields ?? []).map((f) => inputFieldNode(parentId, f, role)))
  }
  // OBJECT / INTERFACE
  return sortNodes(
    (t.fields ?? []).filter((f) => !f.name.startsWith('__')).map((f) => gqlFieldNode(parentId, typeName, f, role)),
  )
}

function gqlFieldNode(parentId: string, ownerType: string, f: IntroField, _role?: string): SchemaNode {
  const u = unwrap(f.type)
  const returnKind = u?.kind ?? 'SCALAR'
  const returnName = u?.name ?? ''
  // Introspection meta-types (`__Type` etc.) are legitimate return types (e.g.
  // `_types: [__Type!]`) — allow expanding them; only field *names* starting
  // with `__` are filtered out (see gqlTypeChildren).
  const returnExpandable = (returnKind === 'OBJECT' || returnKind === 'INTERFACE') && !!returnName
  const hasArgs = f.args.length > 0
  const id = `${parentId}>${f.name}`
  return {
    id,
    label: f.name,
    kind: 'field',
    typeLabel: renderTypeRef(f.type),
    badges: hugrBadge(f.hugr_type ?? undefined),
    hasDescription: !!f.description,
    expandable: hasArgs || returnExpandable,
    selectable: true,
    load:
      hasArgs || returnExpandable
        ? { t: 'gqlField', returnName, returnKind, args: f.args, fieldId: id }
        : undefined,
    detail: { t: 'gqlField', typeName: ownerType, name: f.name, typeLabel: renderTypeRef(f.type) },
  }
}

async function gqlFieldChildren(
  spec: Extract<LoadSpec, { t: 'gqlField' }>,
  role?: string,
): Promise<SchemaNode[]> {
  const out: SchemaNode[] = []
  if (spec.args.length > 0) {
    const argNodes = spec.args.map((a) => argNode(`${spec.fieldId}>args`, a, role))
    out.push({
      id: `${spec.fieldId}>args`,
      label: `args (${spec.args.length})`,
      kind: 'group',
      expandable: true,
      selectable: false,
      load: { t: 'static', children: argNodes },
    })
  }
  const returnExpandable =
    (spec.returnKind === 'OBJECT' || spec.returnKind === 'INTERFACE') && !!spec.returnName
  if (returnExpandable) {
    const fields = await gqlTypeChildren(`${spec.fieldId}>ret`, spec.returnName, role)
    out.push(...fields)
  }
  return out
}

function argNode(parentId: string, a: RawArg, _role?: string): SchemaNode {
  const u = unwrap(a.type)
  const expandable = u?.kind === 'INPUT_OBJECT' || u?.kind === 'ENUM'
  return {
    id: `${parentId}>${a.name}`,
    label: a.name,
    kind: 'arg',
    typeLabel: renderTypeRef(a.type) + (a.defaultValue ? ` = ${a.defaultValue}` : ''),
    expandable,
    selectable: false,
    load: expandable ? { t: 'gqlType', name: u?.name ?? '' } : undefined,
  }
}

function inputFieldNode(parentId: string, f: RawArg, _role?: string): SchemaNode {
  const u = unwrap(f.type)
  const expandable = u?.kind === 'INPUT_OBJECT' || u?.kind === 'ENUM'
  return {
    id: `${parentId}>${f.name}`,
    label: f.name,
    kind: 'inputField',
    typeLabel: renderTypeRef(f.type) + (f.defaultValue ? ` = ${f.defaultValue}` : ''),
    expandable,
    selectable: false,
    load: expandable ? { t: 'gqlType', name: u?.name ?? '' } : undefined,
  }
}

// ═══════════════════════════════════════════════════════════════════════════
// Logical tree — _catalog / _module / _dataObject / _function
// ═══════════════════════════════════════════════════════════════════════════

interface LModule {
  name: string
  dataObjects: { name: string; type: string; description?: string | null; dataSourceName?: string | null }[]
  functions: { name: string; type: string; isTable: boolean; dataSourceName?: string | null }[]
  modules: { name: string }[]
}

const MODULE_BODY = `dataObjects { name type description dataSourceName } functions { name type isTable dataSourceName } modules { name }`

async function loadLogicalRoots(role?: string): Promise<SchemaNode[]> {
  return withDemo(
    () => demoLogicalRoots(),
    async () => {
      const d = await postGraphQL<{ _catalog: LModule & { dataSources: string[] } }>(
        `{ _catalog { name dataSources ${MODULE_BODY} } }`,
        {},
        roleHeaders(role),
      )
      return d._catalog ? moduleBodyToNodes('l', d._catalog, true) : []
    },
  )
}

async function logicalModuleChildren(parentId: string, name: string, role?: string): Promise<SchemaNode[]> {
  const d = await postGraphQL<{ _module: LModule | null }>(
    `query ($name: String!) { _module(name: $name) { name ${MODULE_BODY} } }`,
    { name },
    roleHeaders(role),
  )
  if (!d._module) return []
  return moduleBodyToNodes(parentId, d._module, false)
}

/**
 * Build a module's children as fixed-order category groups — Modules,
 * DataObjects, Functions (each holding alphabetically-sorted items). At the
 * catalog root, also expose the global System / Source type lists (lazy, via
 * `_types(scope)`).
 */
function moduleBodyToNodes(parentId: string, m: LModule, isRoot: boolean): SchemaNode[] {
  const modules = sortNodes(
    (m.modules ?? []).map((sm) => ({
      id: `${parentId}>m:${sm.name}`,
      label: shortModuleName(sm.name, m.name),
      kind: 'module' as const,
      expandable: true,
      selectable: true,
      load: { t: 'logicalModule' as const, name: sm.name },
      detail: { t: 'logicalModule' as const, name: sm.name },
    })),
  )
  // Objects expand into Relations / Fields groups (drill through the relation
  // graph recursively); the data source is shown after the type.
  const objects = sortNodes(
    (m.dataObjects ?? []).map((o) => ({
      id: `${parentId}>o:${o.name}`,
      label: o.name,
      kind: (o.type === 'VIEW' ? 'view' : 'object') as NodeKind,
      typeLabel: withSource(o.type, o.dataSourceName),
      hasDescription: !!o.description,
      expandable: true,
      selectable: true,
      load: { t: 'logicalObjectExpand' as const, name: o.name },
      detail: { t: 'logicalObject' as const, name: o.name },
    })),
  )
  const functions = sortNodes(
    (m.functions ?? []).map((f) => ({
      id: `${parentId}>f:${f.name}`,
      label: f.name,
      kind: 'function' as const,
      typeLabel: withSource(f.isTable ? 'table function' : f.type.toLowerCase(), f.dataSourceName),
      expandable: false,
      selectable: true,
      detail: { t: 'logicalFunction' as const, module: m.name, name: f.name },
    })),
  )

  const groups: SchemaNode[] = []
  if (modules.length) groups.push(staticGroup(parentId, 'modules', 'Modules', modules))
  if (objects.length) groups.push(staticGroup(parentId, 'objects', 'DataObjects', objects))
  if (functions.length) groups.push(staticGroup(parentId, 'functions', 'Functions', functions))
  if (isRoot) {
    groups.push(typesGroup(parentId, 'system', 'System Types', 'SYSTEM'))
    groups.push(typesGroup(parentId, 'source', 'Source Types', 'SOURCE'))
  }
  return groups
}

/** A fixed category header holding pre-built, sorted children. */
function staticGroup(parentId: string, key: string, label: string, children: SchemaNode[]): SchemaNode {
  return {
    id: `${parentId}>g:${key}`,
    label: `${label} · ${children.length}`,
    kind: 'group',
    expandable: children.length > 0,
    selectable: false,
    load: { t: 'static', children },
  }
}

/** A category header whose type list is fetched lazily via `_types(scope)`. */
function typesGroup(parentId: string, key: string, label: string, scope: 'SYSTEM' | 'SOURCE'): SchemaNode {
  return {
    id: `${parentId}>g:${key}`,
    label,
    kind: 'group',
    expandable: true,
    selectable: false,
    load: { t: 'logicalTypes', scope },
  }
}

const EXPANDABLE_KINDS = new Set(['OBJECT', 'INTERFACE', 'INPUT_OBJECT', 'ENUM'])

async function logicalTypesChildren(
  parentId: string,
  scope: 'SYSTEM' | 'SOURCE',
  role?: string,
): Promise<SchemaNode[]> {
  // scope is a fixed enum literal (SYSTEM | SOURCE) — inline it (meta-query enum
  // args are unreliable via GraphQL variables), so no injection surface.
  const d = await postGraphQL<{ _types: { name: string; kind: string }[] | null }>(
    `{ _types(scope: ${scope}) { name kind } }`,
    {},
    roleHeaders(role),
  )
  return sortNodes(
    (d._types ?? []).map((t) => {
      const expandable = EXPANDABLE_KINDS.has(t.kind)
      return {
        id: `${parentId}>t:${t.name}`,
        label: t.name,
        kind: 'object' as NodeKind,
        typeLabel: t.kind,
        expandable,
        selectable: true,
        load: expandable ? ({ t: 'gqlType', name: t.name } as const) : undefined,
        detail: { t: 'gqlType', name: t.name } as const,
      }
    }),
  )
}

/** Trim `parent.child` → `child` for readability when nested under its parent. */
function shortModuleName(full: string, parent: string): string {
  if (parent && full.startsWith(parent + '.')) return full.slice(parent.length + 1)
  return full
}

interface LRelation {
  name: string
  direction: string
  kind: string
  fieldName: string
  dataObject: { name: string; dataSourceName?: string | null } | null
  through: { name: string } | null
}

interface LObjField {
  name: string
  description: string | null
  hugr_type?: string | null
  type: IntroTypeRef
}

function withSource(type: string, source?: string | null): string {
  return source ? `${type} · ${source}` : type
}

/**
 * A data object's children: a Relations group (each relation drills into the
 * far object, recursively) and a Fields group. Fields that reference another
 * data object (relations, `@join`, table-function-call joins) are themselves
 * expandable and drill into that object.
 */
async function logicalObjectExpandChildren(parentId: string, objName: string, role?: string): Promise<SchemaNode[]> {
  const d = await postGraphQL<{
    _dataObject: { relations: LRelation[]; fields: LObjField[] | null } | null
  }>(
    `query ($name: String!) {
      _dataObject(name: $name) {
        relations { name direction kind fieldName dataObject { name dataSourceName } through { name } }
        fields { name description hugr_type type { ${REF} } }
      }
    }`,
    { name: objName },
    roleHeaders(role),
  )
  const o = d._dataObject
  if (!o) return []
  const relNodes = sortNodes((o.relations ?? []).map((r) => relationNode(parentId, r)))
  const fieldNodes = sortNodes(
    (o.fields ?? []).filter((f) => !f.name.startsWith('__')).map((f) => objFieldNode(parentId, objName, f)),
  )
  const groups: SchemaNode[] = []
  if (relNodes.length) groups.push(staticGroup(parentId, 'rel', 'Relations', relNodes))
  if (fieldNodes.length) groups.push(staticGroup(parentId, 'fld', 'Fields', fieldNodes))
  return groups
}

const REL_TONE: Record<string, BadgeTone> = { M2M: 'blue', JOIN: 'amber', FK: 'neutral' }

function relationNode(parentId: string, r: LRelation): SchemaNode {
  const target = r.dataObject?.name ?? ''
  const src = r.dataObject?.dataSourceName
  const arrow = r.direction === 'BACK' ? '←' : '→'
  return {
    id: `${parentId}>r:${r.name}`,
    label: r.fieldName || r.name,
    kind: 'relation',
    typeLabel: `${arrow} ${target}${src ? ` · ${src}` : ''}`,
    badges: [{ text: r.kind, tone: REL_TONE[r.kind] ?? 'neutral' }],
    expandable: !!target,
    selectable: !!target,
    load: target ? { t: 'logicalObjectExpand', name: target } : undefined,
    detail: target ? { t: 'logicalObject', name: target } : undefined,
  }
}

function objFieldNode(parentId: string, ownerName: string, f: LObjField): SchemaNode {
  const u = unwrap(f.type)
  const retName = u?.name ?? ''
  const retKind = u?.kind
  // A field references a data object when it returns an OBJECT whose name is not
  // an engine-generated helper (`_xxx_aggregation`, `_join`, …) — i.e. a real
  // table/view: relations, `@join` fields, table-function-call joins.
  const isObjRef = (retKind === 'OBJECT' || retKind === 'INTERFACE') && !!retName && !retName.startsWith('_')
  return {
    id: `${parentId}>f:${f.name}`,
    label: f.name,
    kind: 'field',
    typeLabel: renderTypeRef(f.type),
    badges: hugrBadge(f.hugr_type ?? undefined),
    hasDescription: !!f.description,
    expandable: isObjRef,
    selectable: true,
    load: isObjRef ? { t: 'logicalObjectExpand', name: retName } : undefined,
    detail: { t: 'gqlField', typeName: ownerName, name: f.name, typeLabel: renderTypeRef(f.type) },
  }
}

// ═══════════════════════════════════════════════════════════════════════════
// Detail
// ═══════════════════════════════════════════════════════════════════════════

export async function loadDetail(node: SchemaNode, role?: string): Promise<NodeDetail | null> {
  if (!node.detail) return null
  // The demo tree is static (no backend); synthesise a detail from the node so
  // selecting a node offline doesn't hit the network.
  if (isDemoMode()) return demoDetail(node)
  const spec = node.detail
  switch (spec.t) {
    case 'gqlType':
      return gqlTypeDetail(spec.name, node, role)
    case 'gqlField':
      return gqlFieldDetail(spec, node, role)
    case 'logicalModule':
      return logicalModuleDetail(spec.name, node, role)
    case 'logicalObject':
      return logicalObjectDetail(spec.name, node, role)
    case 'logicalFunction':
      return logicalFunctionDetail(spec.module, spec.name, node, role)
  }
}

/** hugr_type · N args sub-line for a field row. */
function fieldExtra(f: IntroField): string | undefined {
  const parts: string[] = []
  if (f.hugr_type) parts.push(f.hugr_type)
  if (f.args.length) parts.push(`${f.args.length} arg${f.args.length === 1 ? '' : 's'}`)
  return parts.length ? parts.join(' · ') : undefined
}

function toDetailFields(fields: IntroField[]): DetailField[] {
  return fields.map((f, i) => ({
    ordinal: i + 1,
    name: f.name,
    type: renderTypeRef(f.type),
    description: f.description ?? '',
    extra: fieldExtra(f),
  }))
}

function toArgFields(args: RawArg[]): DetailField[] {
  return args.map((a, i) => ({
    ordinal: i + 1,
    name: a.name,
    type: renderTypeRef(a.type),
    description: a.description ?? '',
    extra: a.defaultValue ? `default: ${a.defaultValue}` : undefined,
  }))
}

const KIND_TONE: Record<string, BadgeTone> = { OBJECT: 'accent', INPUT_OBJECT: 'blue', ENUM: 'amber', SCALAR: 'neutral', INTERFACE: 'accent', UNION: 'blue' }

async function gqlTypeDetail(name: string, node: SchemaNode, role?: string): Promise<NodeDetail> {
  const t = await introspectType(name, role)
  const badges: NodeBadge[] = [{ text: t.kind, tone: KIND_TONE[t.kind] ?? 'neutral' }]
  if (t.hugr_type) badges.push({ text: t.hugr_type, tone: HUGR_TYPE_TONE[t.hugr_type] ?? 'neutral' })
  if (t.catalog) badges.push({ text: `catalog: ${t.catalog}`, tone: 'neutral' })
  if (t.module) badges.push({ text: `module: ${t.module}`, tone: 'neutral' })

  const fields =
    t.kind === 'INPUT_OBJECT'
      ? toArgFields(t.inputFields ?? [])
      : toDetailFields((t.fields ?? []).filter((f) => !f.name.startsWith('__')))
  const metaBits = [
    fields.length ? `${fields.length} fields` : '',
    t.enumValues?.length ? `${t.enumValues.length} values` : '',
  ].filter(Boolean)

  const isRoot = node.kind === 'root'
  return {
    id: node.id,
    name: node.label,
    kind: node.kind,
    badges,
    meta: metaBits.join(' · '),
    description: t.description ?? '',
    fields,
    fieldsLabel: 'Fields',
    enumValues: (t.enumValues ?? []).map((e) => ({ name: e.name, description: e.description ?? '' })),
    relations: [],
    saveKind: isRoot ? null : 'type',
  }
}

async function gqlFieldDetail(
  spec: Extract<DetailSpec, { t: 'gqlField' }>,
  node: SchemaNode,
  role?: string,
): Promise<NodeDetail> {
  // Read the field off its owner type: description + args, and (if it returns an
  // object) the return type's fields, so selecting a field shows what it yields.
  let field: IntroField | undefined
  try {
    const t = await introspectType(spec.typeName, role)
    field = (t.fields ?? []).find((f) => f.name === spec.name)
  } catch {
    /* leave undefined */
  }
  const retName = field ? unwrap(field.type)?.name ?? '' : ''
  const retKind = field ? unwrap(field.type)?.kind : undefined
  let retFields: IntroField[] = []
  if (retName && (retKind === 'OBJECT' || retKind === 'INTERFACE')) {
    try {
      const rt = await introspectType(retName, role)
      retFields = (rt.fields ?? []).filter((f) => !f.name.startsWith('__'))
    } catch {
      /* leave empty */
    }
  }
  const badges: NodeBadge[] = [{ text: spec.typeLabel, tone: 'neutral' }, { text: `field of ${spec.typeName}`, tone: 'neutral' }]
  if (field?.hugr_type) badges.unshift({ text: field.hugr_type, tone: HUGR_TYPE_TONE[field.hugr_type] ?? 'neutral' })
  return {
    id: node.id,
    name: spec.name,
    kind: 'field',
    badges,
    meta: retName ? `returns ${renderTypeRef(field!.type)}` : '',
    description: field?.description ?? '',
    args: field?.args.length ? toArgFields(field.args) : undefined,
    fields: toDetailFields(retFields),
    fieldsLabel: 'Return fields',
    relations: [],
    saveKind: 'field',
    typeName: spec.typeName,
  }
}

async function logicalModuleDetail(name: string, node: SchemaNode, role?: string): Promise<NodeDetail> {
  let description = ''
  try {
    const d = await postGraphQL<{ _module: { description: string | null } | null }>(
      `query ($name: String!) { _module(name: $name) { description } }`,
      { name },
      roleHeaders(role),
    )
    description = d._module?.description ?? ''
  } catch {
    /* leave blank */
  }
  return {
    id: node.id,
    name,
    kind: 'module',
    badges: [{ text: 'MODULE', tone: 'blue' }],
    meta: name,
    description,
    fields: [],
    relations: [],
    saveKind: 'module',
  }
}

async function logicalObjectDetail(name: string, node: SchemaNode, role?: string): Promise<NodeDetail> {
  const d = await postGraphQL<{
    _dataObject: {
      type: string
      description: string | null
      primaryKey: string[] | null
      dataSourceName: string | null
      properties: Record<string, boolean> | null
      fields: LObjField[] | null
      args: LObjField[] | null
      relations: LRelation[]
    } | null
  }>(
    `query ($name: String!) {
      _dataObject(name: $name) {
        type description primaryKey dataSourceName
        properties { isCube isM2M isHypertable softDelete hasVectors }
        fields { name description type { ${REF} } }
        args { name description type { ${REF} } }
        relations { name direction kind fieldName dataObject { name } through { name } }
      }
    }`,
    { name },
    roleHeaders(role),
  )
  const o = d._dataObject
  const badges: NodeBadge[] = [{ text: o?.type === 'VIEW' ? 'VIEW' : '◎ TABLE', tone: o?.type === 'VIEW' ? 'green' : 'accent' }]
  if (o?.properties) {
    for (const [k, on] of Object.entries(o.properties)) if (on) badges.push({ text: k, tone: 'blue' })
  }
  const toLObjFields = (list: LObjField[] | null | undefined): DetailField[] =>
    (list ?? []).map((f, i) => ({ ordinal: i + 1, name: f.name, type: renderTypeRef(f.type), description: f.description ?? '' }))
  return {
    id: node.id,
    name,
    kind: node.kind,
    badges,
    meta: o?.dataSourceName ? `source: ${o.dataSourceName}` : '',
    description: o?.description ?? '',
    fields: toLObjFields(o?.fields),
    fieldsLabel: 'Fields',
    args: o?.args?.length ? toLObjFields(o.args) : undefined,
    relations: (o?.relations ?? []).map((r) => ({
      name: r.fieldName || r.name,
      target: r.dataObject?.name ?? '',
      direction: r.direction === 'BACK' ? ('in' as const) : ('out' as const),
      kind: r.kind,
    })),
    primaryKey: o?.primaryKey ?? undefined,
    dataSource: o?.dataSourceName ?? undefined,
    saveKind: 'type',
  }
}

async function logicalFunctionDetail(module: string, name: string, node: SchemaNode, role?: string): Promise<NodeDetail> {
  const d = await postGraphQL<{
    _function: {
      type: string
      isTable: boolean
      description: string | null
      args: { name: string; description: string | null; type: IntroTypeRef }[]
      returns: IntroTypeRef | null
    } | null
  }>(
    `query ($module: String!, $name: String!) {
      _function(module: $module, name: $name) {
        type isTable description
        args { name description type { ${REF} } }
        returns { ${REF} }
      }
    }`,
    { module, name },
    roleHeaders(role),
  )
  const fn = d._function
  return {
    id: node.id,
    name,
    kind: 'function',
    badges: [
      { text: fn?.type ?? 'FUNCTION', tone: 'amber' },
      ...(fn?.isTable ? [{ text: 'table', tone: 'green' as BadgeTone }] : []),
    ],
    meta: fn?.returns ? `returns ${renderTypeRef(fn.returns)}` : '',
    description: fn?.description ?? '',
    args: fn?.args?.length
      ? fn.args.map((a, i) => ({ ordinal: i + 1, name: a.name, type: renderTypeRef(a.type), description: a.description ?? '' }))
      : undefined,
    fields: [],
    relations: [],
    saveKind: null,
  }
}

// ═══════════════════════════════════════════════════════════════════════════
// Description save (unchanged wiring; the backing API is expected to change)
// ═══════════════════════════════════════════════════════════════════════════

interface OpResult {
  success: boolean
  message: string
}

export async function saveDescription(input: SaveDescriptionInput): Promise<OpResult> {
  const longDescription = input.longDescription ?? input.description
  return withDemo(
    () => ({ success: true, message: `${saveOpName(input)} → saved` }),
    async () => {
      if (input.kind === 'field') {
        const d = await postGraphQL<{ function: { core: { _schema_update_field_desc: OpResult } } }>(
          `mutation ($type_name:String!,$name:String!,$description:String!,$long_description:String!){
            function { core { _schema_update_field_desc(type_name:$type_name, name:$name, description:$description, long_description:$long_description){ success message } } }
          }`,
          {
            type_name: input.target.typeName ?? '',
            name: input.target.name,
            description: input.description,
            long_description: longDescription,
          },
        )
        return d.function.core._schema_update_field_desc
      }
      const fn = `_schema_update_${input.kind}_desc`
      const d = await postGraphQL<{ function: { core: Record<string, OpResult> } }>(
        `mutation ($name:String!,$description:String!,$long_description:String!){
          function { core { ${fn}(name:$name, description:$description, long_description:$long_description){ success message } } }
        }`,
        { name: input.target.name, description: input.description, long_description: longDescription },
      )
      return d.function.core[fn]
    },
  )
}

export function saveOpName(input: SaveDescriptionInput): string {
  if (input.kind === 'field') {
    return `_schema_update_field_desc(type_name:"${input.target.typeName ?? ''}", name:"${input.target.name}")`
  }
  return `_schema_update_${input.kind}_desc(name:"${input.target.name}")`
}

// ═══════════════════════════════════════════════════════════════════════════
// Demo (compact — both trees browsable offline)
// ═══════════════════════════════════════════════════════════════════════════

function demoDetail(node: SchemaNode): NodeDetail {
  const base: NodeDetail = {
    id: node.id,
    name: node.label,
    kind: node.kind,
    badges: [],
    meta: '',
    description: '',
    fields: [],
    relations: [],
    saveKind: null,
  }
  const spec = node.detail
  if (!spec) return base
  switch (spec.t) {
    case 'gqlType':
      return { ...base, badges: [{ text: node.kind === 'root' ? 'ROOT' : '◎ OBJECT', tone: 'accent' }], saveKind: node.kind === 'root' ? null : 'type' }
    case 'gqlField':
      return { ...base, name: spec.name, badges: [{ text: spec.typeLabel, tone: 'neutral' }], saveKind: 'field', typeName: spec.typeName }
    case 'logicalObject':
      return {
        ...base,
        badges: [{ text: '◎ TABLE', tone: 'accent' }],
        meta: 'source: demo',
        primaryKey: ['id'],
        relations: [{ name: 'related', target: 'other_object', direction: 'out', kind: 'FK' }],
        saveKind: 'type',
      }
    case 'logicalModule':
      return { ...base, badges: [{ text: 'MODULE', tone: 'blue' }], saveKind: 'module' }
    case 'logicalFunction':
      return { ...base, badges: [{ text: 'FUNCTION', tone: 'amber' }], saveKind: null }
  }
}

function demoGraphqlRoots(): SchemaNode[] {
  return (['Query', 'Mutation', 'Subscription'] as const).map((label) => {
    const children = label === 'Query' ? demoGqlQueryModules() : []
    return {
      id: `g:${label}`,
      label,
      kind: 'root' as const,
      typeLabel: label,
      expandable: children.length > 0,
      selectable: true,
      load: { t: 'static' as const, children },
      detail: { t: 'gqlType' as const, name: label },
    }
  })
}

function demoGqlQueryModules(): SchemaNode[] {
  return ['core', 'analytics', 'geo'].map((m) => ({
    id: `g:Query>${m}`,
    label: m,
    kind: 'field' as const,
    typeLabel: `_module_${m}_query`,
    badges: [{ text: 'select', tone: 'accent' as BadgeTone }],
    expandable: false,
    selectable: true,
    detail: { t: 'gqlType' as const, name: `_module_${m}_query` },
  }))
}

function demoLogicalRoots(): SchemaNode[] {
  const mk = (name: string): SchemaNode => ({
    id: `l>m:${name}`,
    label: name,
    kind: 'module',
    expandable: true,
    selectable: true,
    load: { t: 'static', children: demoLogicalObjects(name) },
    detail: { t: 'logicalModule', name },
  })
  return [mk('core'), mk('analytics'), mk('geo')]
}

function demoLogicalObjects(mod: string): SchemaNode[] {
  const objs: Record<string, string[]> = {
    core: ['core_data_sources', 'core_catalog_sources', 'core_roles'],
    analytics: ['events', 'sessions', 'users'],
    geo: ['regions', 'cities'],
  }
  return (objs[mod] ?? []).map((name) => ({
    id: `l>m:${mod}>o:${name}`,
    label: name,
    kind: 'object' as const,
    typeLabel: 'TABLE',
    expandable: false,
    selectable: true,
    detail: { t: 'logicalObject' as const, name },
  }))
}
