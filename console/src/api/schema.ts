import { postGraphQL } from '@/lib/graphql'
import { withDemo, isDemoMode } from '@/lib/demo'

/**
 * Schema Explorer data layer.
 *
 * The console browses Hugr's *unified GraphQL schema* as a lazily-expanded tree
 * (Query / Mutation → modules → generated ops → fields / relations / args) and
 * lets an admin edit descriptions that feed the LLM schema summaries via the
 * `core._schema_update_{type,field,module,catalog}_desc` mutations.
 *
 * REAL wiring: the tree is best-effort GraphQL introspection (`__type`), which
 * cannot fully reconstruct Hugr's module classification (table vs view vs
 * function) or its relation graph — those live in `core.meta` / the describe
 * functions. Those gaps are marked `// TODO(real)`. The DEMO mock is a full,
 * interactive stand-in so the screen looks and behaves correctly offline.
 */

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type SchemaNodeKind =
  | 'query'
  | 'mutation'
  | 'module'
  | 'table'
  | 'view'
  | 'function'
  | 'field'
  | 'relation'

export interface SchemaNode {
  id: string
  name: string
  kind: SchemaNodeKind
  /** GraphQL type / return type, shown muted after the name (e.g. `[data_sources]`, `String!`). */
  typeLabel?: string
  /** true when the node carries a saved description (renders the accent dot). */
  hasDescription?: boolean
  /** true when the node can be expanded to load children. */
  expandable?: boolean
  childrenLoaded?: boolean
  children?: SchemaNode[]
  args?: SchemaNode[]
  fields?: SchemaNode[]
  relations?: SchemaNode[]
  /** module the node belongs to — resolves the save target. */
  module?: string
}

export type BadgeTone = 'neutral' | 'green' | 'amber' | 'red' | 'blue' | 'accent'

export interface DetailBadge {
  text: string
  tone: BadgeTone
}

export interface DetailField {
  ordinal: number
  name: string
  type: string
  description: string
}

export interface DetailRelation {
  name: string
  target: string
  direction: 'out' | 'in'
}

export interface NodeDetail {
  id: string
  name: string
  kind: SchemaNodeKind
  badges: DetailBadge[]
  meta: string
  description: string
  fields: DetailField[]
  relations: DetailRelation[]
  /** which `_schema_update_*_desc` mutation backs this node's description save (null = read-only). */
  saveKind: SaveKind | null
  /** GraphQL type name the field save targets (`_schema_update_field_desc(type_name)`). */
  typeName?: string
}

export interface SchemaHit {
  id: string
  name: string
  kind: 'table' | 'view' | 'function'
  module: string
  fieldCount: number
}

export type SaveKind = 'type' | 'field' | 'module' | 'catalog'

export interface SaveDescriptionInput {
  kind: SaveKind
  /** entity name; `typeName` is required when kind === 'field'. */
  target: { name: string; typeName?: string }
  description: string
  /** long form fed to the summarizer — defaults to `description` when omitted. */
  longDescription?: string
}

// ---------------------------------------------------------------------------
// Demo mock — a substantial unified schema (Query/Mutation → core/analytics/geo)
// ---------------------------------------------------------------------------

interface MockField {
  name: string
  type: string
  description?: string
}
interface MockRelation {
  name: string
  target: string
  direction: 'out' | 'in'
}
interface MockType {
  name: string
  kind: 'table' | 'view' | 'function'
  description?: string
  returns?: string
  fields: MockField[]
  relations?: MockRelation[]
}
interface MockModule {
  name: string
  description?: string
  types: MockType[]
}

const QUERY_MODULES: MockModule[] = [
  {
    name: 'core',
    description:
      'Platform administration surface: data sources, catalogs, roles, permissions and schema metadata.',
    types: [
      {
        name: 'data_sources',
        kind: 'table',
        description:
          'Registered data sources (postgres, duckdb, http, …) with their connection and module settings.',
        fields: [
          { name: 'name', type: 'String!', description: 'Primary key — unique source name.' },
          { name: 'type', type: 'String!', description: 'Connector type, e.g. postgres, duckdb, http.' },
          { name: 'prefix', type: 'String' },
          { name: 'path', type: 'String', description: 'DSN / connection path.' },
          { name: 'description', type: 'String' },
          { name: 'as_module', type: 'Boolean' },
          { name: 'read_only', type: 'Boolean' },
          { name: 'disabled', type: 'Boolean' },
          { name: 'self_defined', type: 'Boolean' },
        ],
        relations: [{ name: 'catalogs', target: 'catalog_sources', direction: 'out' }],
      },
      {
        name: 'catalog_sources',
        kind: 'table',
        description: 'Attachable catalogs (localFS / uri) linked to data sources.',
        fields: [
          { name: 'name', type: 'String!' },
          { name: 'type', type: 'String!', description: 'localFS or uri.' },
          { name: 'path', type: 'String' },
          { name: 'description', type: 'String' },
        ],
        relations: [{ name: 'data_sources', target: 'data_sources', direction: 'in' }],
      },
      {
        name: 'role_permissions',
        kind: 'view',
        description:
          'Effective role → (type, field) permission rows. ALLOW-by-default; a row only hides, disables or row-filters.',
        fields: [
          { name: 'role', type: 'String!' },
          { name: 'type_name', type: 'String!' },
          { name: 'field_name', type: 'String!' },
          { name: 'hidden', type: 'Boolean' },
          { name: 'disabled', type: 'Boolean' },
          { name: 'filter', type: 'JSON', description: 'Row-level-security expression (interpolates auth claims).' },
        ],
        relations: [{ name: 'role_def', target: 'roles', direction: 'out' }],
      },
      {
        name: 'data_source_status',
        kind: 'function',
        returns: 'String',
        description: 'Live connection status for a data source (drives the health dot).',
        fields: [{ name: 'name', type: 'String!', description: 'Data source name.' }],
      },
    ],
  },
  {
    name: 'analytics',
    description: 'Product analytics: events, sessions, users and derived rollups.',
    types: [
      {
        name: 'events',
        kind: 'table',
        description: 'Raw product event stream — one row per tracked interaction.',
        fields: [
          { name: 'event_id', type: 'ID!' },
          { name: 'user_id', type: 'String!' },
          { name: 'event_type', type: 'String!', description: 'e.g. page_view, signup, purchase.' },
          { name: 'occurred_at', type: 'Timestamp!' },
          { name: 'properties', type: 'JSON' },
          { name: 'revenue', type: 'Float' },
        ],
        relations: [
          { name: 'user', target: 'users', direction: 'out' },
          { name: 'session', target: 'sessions', direction: 'out' },
        ],
      },
      {
        name: 'sessions',
        kind: 'table',
        description: 'User sessions bounding a run of events.',
        fields: [
          { name: 'session_id', type: 'ID!' },
          { name: 'user_id', type: 'String!' },
          { name: 'started_at', type: 'Timestamp!' },
          { name: 'ended_at', type: 'Timestamp' },
          { name: 'device', type: 'String' },
          { name: 'event_count', type: 'Int' },
        ],
        relations: [
          { name: 'user', target: 'users', direction: 'out' },
          { name: 'events', target: 'events', direction: 'in' },
        ],
      },
      {
        name: 'users',
        kind: 'table',
        description: 'Analytics user dimension.',
        fields: [
          { name: 'user_id', type: 'ID!' },
          { name: 'email', type: 'String!' },
          { name: 'plan', type: 'String' },
          { name: 'country', type: 'String' },
          { name: 'created_at', type: 'Timestamp!' },
        ],
        relations: [
          { name: 'events', target: 'events', direction: 'in' },
          { name: 'sessions', target: 'sessions', direction: 'in' },
        ],
      },
      {
        name: 'daily_active_users',
        kind: 'view',
        description: 'Daily / weekly / monthly active-user rollup.',
        fields: [
          { name: 'day', type: 'Date!' },
          { name: 'dau', type: 'Int!' },
          { name: 'wau', type: 'Int' },
          { name: 'mau', type: 'Int' },
        ],
      },
      {
        name: 'funnel',
        kind: 'function',
        returns: '[funnel_step]',
        description: 'Ordered-step conversion funnel over a time window.',
        fields: [
          { name: 'steps', type: '[String!]!', description: 'Ordered event types.' },
          { name: 'window_days', type: 'Int', description: 'Attribution window (default 30).' },
        ],
      },
    ],
  },
  {
    name: 'geo',
    description: 'Spatial reference data — regions and cities with geometry.',
    types: [
      {
        name: 'regions',
        kind: 'table',
        description: 'Administrative regions with boundary geometry.',
        fields: [
          { name: 'region_id', type: 'ID!' },
          { name: 'name', type: 'String!' },
          { name: 'iso_code', type: 'String' },
          { name: 'geom', type: 'Geometry' },
          { name: 'population', type: 'BigInt' },
        ],
        relations: [{ name: 'cities', target: 'cities', direction: 'in' }],
      },
      {
        name: 'cities',
        kind: 'table',
        description: 'Cities with point locations, linked to a region.',
        fields: [
          { name: 'city_id', type: 'ID!' },
          { name: 'name', type: 'String!' },
          { name: 'region_id', type: 'String!' },
          { name: 'population', type: 'Int' },
          { name: 'location', type: 'Point' },
          { name: 'timezone', type: 'String' },
        ],
        relations: [{ name: 'region', target: 'regions', direction: 'out' }],
      },
      {
        name: 'region_stats',
        kind: 'view',
        description: 'Per-region aggregate counts.',
        fields: [
          { name: 'region_id', type: 'String!' },
          { name: 'city_count', type: 'Int!' },
          { name: 'total_population', type: 'BigInt!' },
        ],
      },
      {
        name: 'nearest_cities',
        kind: 'function',
        returns: '[cities]',
        description: 'K nearest cities to a lat/lon point.',
        fields: [
          { name: 'lat', type: 'Float!' },
          { name: 'lon', type: 'Float!' },
          { name: 'limit', type: 'Int' },
        ],
      },
    ],
  },
]

const MUTATION_MODULES: MockModule[] = [
  {
    name: 'core',
    description: 'Administrative mutations over the platform tables + schema maintenance.',
    types: [
      {
        name: 'insert_data_sources',
        kind: 'function',
        returns: 'data_sources',
        description: 'Register a new data source (supports nested catalogs).',
        fields: [{ name: 'data', type: 'data_sources_mut_input!' }],
      },
      {
        name: 'update_data_sources',
        kind: 'function',
        returns: 'data_sources',
        fields: [
          { name: 'filter', type: 'data_sources_filter' },
          { name: 'data', type: 'data_sources_mut_input!' },
        ],
      },
      {
        name: 'delete_data_sources',
        kind: 'function',
        returns: 'OperationResult',
        fields: [{ name: 'filter', type: 'data_sources_filter' }],
      },
      {
        name: '_schema_reindex',
        kind: 'function',
        returns: 'OperationResult',
        description: 'Recompute embeddings for schema entities.',
        fields: [
          { name: 'name', type: 'String' },
          { name: 'batch_size', type: 'Int' },
        ],
      },
    ],
  },
  {
    name: 'analytics',
    description: 'Analytics write surface.',
    types: [
      {
        name: 'insert_events',
        kind: 'function',
        returns: 'events',
        fields: [{ name: 'data', type: '[events_mut_input!]!' }],
      },
      {
        name: 'update_sessions',
        kind: 'function',
        returns: 'sessions',
        fields: [
          { name: 'filter', type: 'sessions_filter' },
          { name: 'data', type: 'sessions_mut_input!' },
        ],
      },
    ],
  },
]

// Node ids: `${root}` · `${root}:${module}` · `${root}:${module}:${type}` ·
// `${typeId}#${field}` (field) · `${typeId}~${relation}` (relation).
const MOCK_INDEX = new Map<string, { children: SchemaNode[]; detail: NodeDetail }>()

function kindChipTone(kind: SchemaNodeKind): BadgeTone {
  switch (kind) {
    case 'query':
    case 'table':
      return 'accent'
    case 'mutation':
    case 'function':
      return 'amber'
    case 'view':
      return 'green'
    case 'module':
    case 'relation':
      return 'blue'
    default:
      return 'neutral'
  }
}

function typeBadges(t: MockType, moduleName: string): DetailBadge[] {
  const head =
    t.kind === 'table'
      ? { text: '◎ OBJECT', tone: 'accent' as BadgeTone }
      : { text: t.kind.toUpperCase(), tone: kindChipTone(t.kind) }
  const badges: DetailBadge[] = [head, { text: `module: ${moduleName}`, tone: 'neutral' }]
  if (t.returns) badges.push({ text: `returns: ${t.returns}`, tone: 'neutral' })
  return badges
}

function buildType(rootId: string, moduleName: string, t: MockType): SchemaNode {
  const typeId = `${rootId}:${moduleName}:${t.name}`
  const isFn = t.kind === 'function'

  const fieldNodes: SchemaNode[] = t.fields.map((f) => ({
    id: `${typeId}#${f.name}`,
    name: f.name,
    kind: 'field',
    typeLabel: f.type,
    hasDescription: !!f.description,
    expandable: false,
    module: moduleName,
  }))

  const relationNodes: SchemaNode[] = (t.relations ?? []).map((r) => ({
    id: `${typeId}~${r.name}`,
    name: r.name,
    kind: 'relation',
    typeLabel: `${r.direction === 'in' ? '←' : '→'} ${r.target}`,
    expandable: false,
    module: moduleName,
  }))

  const children = [...fieldNodes, ...relationNodes]

  const detailFields: DetailField[] = t.fields.map((f, i) => ({
    ordinal: i + 1,
    name: f.name,
    type: f.type,
    description: f.description ?? '',
  }))
  const detailRelations: DetailRelation[] = (t.relations ?? []).map((r) => ({
    name: r.name,
    target: r.target,
    direction: r.direction,
  }))

  const meta = isFn
    ? `${t.fields.length} arg${t.fields.length === 1 ? '' : 's'} · returns ${t.returns ?? 'void'}`
    : `${t.fields.length} field${t.fields.length === 1 ? '' : 's'} · ${detailRelations.length} relation${
        detailRelations.length === 1 ? '' : 's'
      }`

  MOCK_INDEX.set(typeId, {
    children,
    detail: {
      id: typeId,
      name: t.name,
      kind: t.kind,
      badges: typeBadges(t, moduleName),
      meta,
      description: t.description ?? '',
      fields: detailFields,
      relations: detailRelations,
      saveKind: 'type',
    },
  })

  // Field-level detail (editable via _schema_update_field_desc).
  t.fields.forEach((f) => {
    const fid = `${typeId}#${f.name}`
    MOCK_INDEX.set(fid, {
      children: [],
      detail: {
        id: fid,
        name: f.name,
        kind: 'field',
        badges: [
          { text: f.type, tone: 'neutral' },
          { text: `${isFn ? 'arg of' : 'field of'} ${t.name}`, tone: 'neutral' },
        ],
        meta: `${isFn ? 'argument' : 'field'} · ${moduleName}.${t.name}`,
        description: f.description ?? '',
        fields: [],
        relations: [],
        saveKind: 'field',
        typeName: t.name,
      },
    })
  })

  return {
    id: typeId,
    name: t.name,
    kind: t.kind,
    typeLabel: isFn ? t.returns : undefined,
    hasDescription: !!t.description,
    expandable: children.length > 0,
    module: moduleName,
  }
}

function buildRoot(rootId: 'query' | 'mutation', modules: MockModule[]): SchemaNode {
  const moduleNodes: SchemaNode[] = modules.map((m) => {
    const moduleId = `${rootId}:${m.name}`
    const typeNodes = m.types.map((t) => buildType(rootId, m.name, t))
    MOCK_INDEX.set(moduleId, {
      children: typeNodes,
      detail: {
        id: moduleId,
        name: m.name,
        kind: 'module',
        badges: [{ text: 'MODULE', tone: 'blue' }],
        meta: `${m.types.length} object${m.types.length === 1 ? '' : 's'}`,
        description: m.description ?? '',
        fields: [],
        relations: [],
        saveKind: 'module',
      },
    })
    return {
      id: moduleId,
      name: m.name,
      kind: 'module',
      typeLabel: `${m.types.length} objects`,
      hasDescription: !!m.description,
      expandable: true,
      module: m.name,
    }
  })

  MOCK_INDEX.set(rootId, {
    children: moduleNodes,
    detail: {
      id: rootId,
      name: rootId === 'query' ? 'Query' : 'Mutation',
      kind: rootId,
      badges: [{ text: rootId === 'query' ? 'QUERY ROOT' : 'MUTATION ROOT', tone: kindChipTone(rootId) }],
      meta: `${modules.length} modules`,
      description: '',
      fields: [],
      relations: [],
      saveKind: null,
    },
  })

  return {
    id: rootId,
    name: rootId === 'query' ? 'Query' : 'Mutation',
    kind: rootId,
    typeLabel: 'unified schema',
    expandable: true,
  }
}

let mockRoots: SchemaNode[] | null = null
function mockGraph(): SchemaNode[] {
  if (!mockRoots) {
    MOCK_INDEX.clear()
    mockRoots = [buildRoot('query', QUERY_MODULES), buildRoot('mutation', MUTATION_MODULES)]
  }
  return mockRoots
}

function mockTypeNodes(): SchemaNode[] {
  mockGraph()
  // Flatten every module's type nodes (Query root only, to avoid duplicates).
  const flat: SchemaNode[] = []
  QUERY_MODULES.forEach((m) => {
    const modChildren = MOCK_INDEX.get(`query:${m.name}`)?.children ?? []
    modChildren.forEach((c) => flat.push({ ...c }))
  })
  return flat
}

// ---------------------------------------------------------------------------
// Public fetchers
// ---------------------------------------------------------------------------

/** Top of the tree: the Query and Mutation operation roots (lazy children). */
export async function loadRootModules(): Promise<SchemaNode[]> {
  return withDemo(
    () => mockGraph().map((n) => ({ ...n })),
    async () => {
      // TODO(real): Hugr's module structure is not standard introspection. We
      // return the two operation roots and let `loadNodeChildren` introspect
      // `__type(Query|Mutation)` for the module fields.
      const roots: SchemaNode[] = [
        { id: 'Query', name: 'Query', kind: 'query', typeLabel: 'unified schema', expandable: true },
        { id: 'Mutation', name: 'Mutation', kind: 'mutation', typeLabel: 'unified schema', expandable: true },
      ]
      return roots
    },
  )
}

/** Flat list of every table / view / function (Model view + search source). */
export async function loadModelTypes(): Promise<SchemaNode[]> {
  return withDemo(
    () => mockTypeNodes().map((n) => ({ ...n })),
    async () => {
      // TODO(real): enumerate types across modules via introspection.
      return []
    },
  )
}

/** Children of a node, fetched on expand. */
export async function loadNodeChildren(nodeId: string): Promise<SchemaNode[]> {
  return withDemo(
    () => (MOCK_INDEX.get(nodeId)?.children ?? []).map((n) => ({ ...n })),
    async () => introspectChildren(nodeId),
  )
}

/** Detail (badges + description + fields + relations) for the selected node. */
export async function getNodeDetail(nodeId: string): Promise<NodeDetail> {
  return withDemo(
    () => {
      const found = MOCK_INDEX.get(nodeId)
      if (found) return { ...found.detail }
      return emptyDetail(nodeId)
    },
    async () => introspectDetail(nodeId),
  )
}

/** Filter tables / views / functions by name + kind (left-panel search). */
export async function searchSchema(query: string, kind: 'all' | 'table' | 'view' | 'function'): Promise<SchemaHit[]> {
  return withDemo(
    () => {
      const q = query.trim().toLowerCase()
      const hits: SchemaHit[] = []
      QUERY_MODULES.forEach((m) =>
        m.types.forEach((t) => {
          if (kind !== 'all' && t.kind !== kind) return
          if (q && !t.name.toLowerCase().includes(q) && !m.name.toLowerCase().includes(q)) return
          hits.push({
            id: `query:${m.name}:${t.name}`,
            name: t.name,
            kind: t.kind,
            module: m.name,
            fieldCount: t.fields.length,
          })
        }),
      )
      return hits
    },
    async () => {
      // TODO(real): search should hit `core.meta` / describe output; introspection
      // cannot classify view vs table vs function precisely.
      void query
      void kind
      return []
    },
  )
}

/**
 * Persist an edited description — feeds the LLM schema summaries. Routes to the
 * right `core._schema_update_{type|field|module|catalog}_desc` mutation.
 */
export async function saveDescription(
  input: SaveDescriptionInput,
): Promise<{ success: boolean; message: string }> {
  const longDescription = input.longDescription ?? input.description
  if (isDemoMode()) {
    // Small delay to exercise the pending state; echo the real op in the toast.
    await new Promise((r) => setTimeout(r, 200))
    return { success: true, message: `${saveOpName(input)} → saved` }
  }

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
    return opResult(d.function.core._schema_update_field_desc)
  }

  const fn = `_schema_update_${input.kind}_desc`
  const d = await postGraphQL<{ function: { core: Record<string, OpResult> } }>(
    `mutation ($name:String!,$description:String!,$long_description:String!){
      function { core { ${fn}(name:$name, description:$description, long_description:$long_description){ success message } } }
    }`,
    { name: input.target.name, description: input.description, long_description: longDescription },
  )
  return opResult(d.function.core[fn])
}

/** A mono, human-readable echo of the backing mutation (used in the toast). */
export function saveOpName(input: SaveDescriptionInput): string {
  if (input.kind === 'field') {
    return `_schema_update_field_desc(type_name:"${input.target.typeName ?? ''}", name:"${input.target.name}")`
  }
  return `_schema_update_${input.kind}_desc(name:"${input.target.name}")`
}

// ---------------------------------------------------------------------------
// Real (best-effort) introspection helpers
// ---------------------------------------------------------------------------

interface OpResult {
  success: boolean
  message: string
}
function opResult(r?: OpResult): { success: boolean; message: string } {
  return { success: r?.success ?? false, message: r?.message ?? '' }
}

interface IntroTypeRef {
  kind: string
  name: string | null
  ofType: IntroTypeRef | null
}
interface IntroField {
  name: string
  description: string | null
  args: { name: string }[]
  type: IntroTypeRef
}

const REF_FRAGMENT = `fragment Ref on __Type { kind name ofType { kind name ofType { kind name ofType { kind name } } } }`

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

async function introspectType(name: string): Promise<IntroField[]> {
  const d = await postGraphQL<{ __type: { fields: IntroField[] | null } | null }>(
    `query IntrospectType($name:String!){ __type(name:$name){ name kind fields { name description args { name } type { ...Ref } } } } ${REF_FRAGMENT}`,
    { name },
  )
  return d.__type?.fields ?? []
}

async function introspectChildren(nodeId: string): Promise<SchemaNode[]> {
  // Leaf field ids look like `Type.field`; they have no children.
  if (nodeId.includes('.')) return []
  const isRoot = nodeId === 'Query' || nodeId === 'Mutation'
  let fields: IntroField[]
  try {
    fields = await introspectType(nodeId)
  } catch {
    return []
  }
  return fields
    .filter((f) => !f.name.startsWith('__'))
    .map((f) => {
      const u = unwrap(f.type)
      const isObject = !!u && u.kind === 'OBJECT' && !!u.name && !u.name.startsWith('__')
      if (isObject) {
        // TODO(real): classify module vs table/view/function via core.meta; we
        // approximate — direct children of a root are modules, deeper are tables.
        const kind: SchemaNodeKind = isRoot ? 'module' : f.args.length ? 'function' : 'table'
        return {
          id: u!.name as string,
          name: f.name,
          kind,
          typeLabel: renderTypeRef(f.type),
          expandable: true,
        }
      }
      return {
        id: `${nodeId}.${f.name}`,
        name: f.name,
        kind: 'field',
        typeLabel: renderTypeRef(f.type),
        expandable: false,
      }
    })
}

async function introspectDetail(nodeId: string): Promise<NodeDetail> {
  // Leaf field: `Type.field`.
  const dot = nodeId.lastIndexOf('.')
  if (dot > 0 && nodeId !== 'Query' && nodeId !== 'Mutation') {
    const typeName = nodeId.slice(0, dot)
    const fieldName = nodeId.slice(dot + 1)
    return {
      id: nodeId,
      name: fieldName,
      kind: 'field',
      badges: [{ text: `field of ${typeName}`, tone: 'neutral' }],
      meta: `field · ${typeName}`,
      description: '', // TODO(real): editable descriptions live in core.meta, not introspection.
      fields: [],
      relations: [],
      saveKind: 'field',
      typeName,
    }
  }

  let fields: IntroField[]
  try {
    fields = await introspectType(nodeId)
  } catch {
    return emptyDetail(nodeId)
  }
  const scalarFields = fields.filter((f) => {
    const u = unwrap(f.type)
    return !u || u.kind !== 'OBJECT'
  })
  const objectFields = fields.filter((f) => {
    const u = unwrap(f.type)
    return u && u.kind === 'OBJECT' && u.name && !u.name.startsWith('__')
  })
  const isRoot = nodeId === 'Query' || nodeId === 'Mutation'
  return {
    id: nodeId,
    name: nodeId,
    kind: isRoot ? (nodeId === 'Query' ? 'query' : 'mutation') : 'table',
    badges: [{ text: isRoot ? 'ROOT' : '◎ OBJECT', tone: isRoot ? 'neutral' : 'accent' }],
    meta: `${scalarFields.length} fields · ${objectFields.length} relations`,
    description: '', // TODO(real): pull from core.meta.
    fields: scalarFields.map((f, i) => ({
      ordinal: i + 1,
      name: f.name,
      type: renderTypeRef(f.type),
      description: f.description ?? '',
    })),
    relations: objectFields.map((f) => ({
      name: f.name,
      target: unwrap(f.type)?.name ?? '',
      direction: 'out' as const,
    })),
    // TODO(real): a root/module has no `_schema_update_type_desc` target; only
    // real object types do. We default to 'type' for object nodes.
    saveKind: isRoot ? null : 'type',
  }
}

function emptyDetail(nodeId: string): NodeDetail {
  return {
    id: nodeId,
    name: nodeId,
    kind: 'table',
    badges: [],
    meta: '',
    description: '',
    fields: [],
    relations: [],
    saveKind: null,
  }
}
