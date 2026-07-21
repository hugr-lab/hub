import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'

/* ────────────────────────────────────────────────────────────────────────
 * Types
 * ──────────────────────────────────────────────────────────────────────── */

export type DataSourceType =
  | 'postgres'
  | 'duckdb'
  | 'mysql'
  | 'mssql'
  | 'http'
  | 'extension'
  | 'ducklake'
  | 'iceberg'
  | 'llm-openai'
  | 'llm-anthropic'
  | 'llm-gemini'

export const DATA_SOURCE_TYPES: DataSourceType[] = [
  'postgres',
  'duckdb',
  'mysql',
  'mssql',
  'http',
  'ducklake',
  'iceberg',
  'extension',
  'llm-openai',
  'llm-anthropic',
  'llm-gemini',
]

/** Runtime connection status derived from `data_source_status(name)`. */
export type DataSourceStatus = 'ready' | 'loading' | 'error' | 'unloaded'

/** Row of `core.data_sources` (config only — runtime status is a separate call). */
export interface DataSource {
  name: string
  type: string
  prefix: string
  as_module: boolean
  path: string
  description: string
  disabled: boolean
  read_only: boolean
  self_defined: boolean
  /** Catalog sources feeding this data source (M2M via `core.catalogs`). */
  catalogs?: { name: string }[]
}

/** A nested catalog attached on `insert_data_sources`. */
export interface NestedCatalog {
  name: string
  type: string
  path: string
}

export interface DataSourceInput {
  name: string
  type: string
  prefix?: string
  path?: string
  description?: string
  as_module?: boolean
  read_only?: boolean
  self_defined?: boolean
  disabled?: boolean
  /** Attach schema catalogs on insert (ignored by update). */
  catalogs?: NestedCatalog[]
}

/** A described type from `describe_data_source_schema(name)`. */
export interface SchemaType {
  name: string
  kind: string
  count: number
  /** Pre-joined mono field line for display. */
  fields: string
}

/**
 * Catalog source kinds. Authoritative list from the engine
 * (`query-engine/pkg/catalog/sources/sources.go`) — `type` is a plain String
 * scalar in the GraphQL schema (no enum to introspect), so this curated set is
 * necessary. Keep in sync if the engine adds a kind.
 */
export const CATALOG_SOURCE_TYPES = ['localFS', 'uri', 'uriFile', 'text'] as const

/** Row of `core.catalog_sources`. */
export interface CatalogSource {
  name: string
  type: (typeof CATALOG_SOURCE_TYPES)[number] | string
  description: string
  path: string
}

export interface CatalogSourceInput {
  name: string
  type?: string
  description?: string
  path?: string
}

/** Row of the M2M `core.catalogs`. */
export interface CatalogLink {
  catalog_name: string
  data_source_name: string
}

/** Imperative-function envelope `{ success, message }`. */
export interface FnResult {
  success: boolean
  message: string
}

/* ────────────────────────────────────────────────────────────────────────
 * Demo backing store — a tiny in-memory backend so the screens are fully
 * interactive offline (`/console/?demo=1`). Mirrors the real op shapes.
 * ──────────────────────────────────────────────────────────────────────── */

const demoDataSources: DataSource[] = [
  { name: 'core-warehouse', type: 'postgres', prefix: 'wh', path: 'postgres://hub@pg-core:5432/warehouse', as_module: true, read_only: false, self_defined: false, disabled: false, description: 'Primary operational warehouse (PostGIS, TimescaleDB).' },
  { name: 'iot-lake', type: 'ducklake', prefix: 'iot', path: 's3://hugr-lake/iot', as_module: true, read_only: true, self_defined: false, disabled: false, description: 'Sensor telemetry lakehouse, snapshot time-travel.' },
  { name: 'billing', type: 'mysql', prefix: 'bill', path: 'mysql://hub@my-bill:3306/billing', as_module: true, read_only: true, self_defined: false, disabled: false, description: 'Billing replica (read-only).' },
  { name: 'geo-files', type: 'duckdb', prefix: 'geo', path: '/data/geo/geo.db', as_module: false, read_only: false, self_defined: false, disabled: false, description: 'GeoParquet + Shapefile workspace.' },
  { name: 'weather-api', type: 'http', prefix: 'wx', path: 'https://api.meteo.internal/v2', as_module: true, read_only: true, self_defined: false, disabled: false, description: 'External REST source (OAuth2).' },
  { name: 'claude', type: 'llm-anthropic', prefix: 'llm', path: 'https://api.anthropic.com', as_module: true, read_only: true, self_defined: true, disabled: false, description: 'LLM data source for talk-to-data.' },
  { name: 'metrics-archive', type: 'iceberg', prefix: 'arch', path: 's3://hugr-archive/metrics', as_module: true, read_only: true, self_defined: false, disabled: true, description: 'Cold Iceberg metrics archive.' },
]

const demoStatuses: Record<string, DataSourceStatus> = {
  'core-warehouse': 'ready',
  'iot-lake': 'ready',
  billing: 'error',
  'geo-files': 'ready',
  'weather-api': 'loading',
  claude: 'ready',
  'metrics-archive': 'unloaded',
}

const demoCatalogSources: CatalogSource[] = [
  { name: 'mesh-core', type: 'localFS', path: '/schemas/core', description: 'Core domain schema definitions.' },
  { name: 'iot-domain', type: 'uri', path: 's3://hugr-schemas/iot', description: 'Sensor fleet domain, owned by data-eng.' },
  { name: 'geo-domain', type: 'localFS', path: '/schemas/geo', description: 'Spatial layers and functions.' },
]

const demoLinks: CatalogLink[] = [
  { catalog_name: 'mesh-core', data_source_name: 'core-warehouse' },
  { catalog_name: 'mesh-core', data_source_name: 'billing' },
  { catalog_name: 'iot-domain', data_source_name: 'iot-lake' },
  { catalog_name: 'geo-domain', data_source_name: 'geo-files' },
]

const DEMO_SDL = `type gateways @table(name: "gateways") {
	id: ID! @pk
	site: String!
	last_seen: Timestamp
	status: String
	location: Geometry
}
type readings @table(name: "readings") {
	gateway_id: String! @field_references(references_name: "gateways", field: "id")
	metric: String!
	value: Float
	at: Timestamp
}
type daily_rollup @view(name: "daily_rollup") {
	day: Date!
	site: String!
	avg_value: Float
	sample_count: Int
}`

const ok = (message: string): FnResult => ({ success: true, message })

/* ────────────────────────────────────────────────────────────────────────
 * Status helpers
 * ──────────────────────────────────────────────────────────────────────── */

/**
 * Map a `data_source_status` string onto a UI status. The engine's live
 * vocabulary is `attached` (loaded & queryable → ready) / `detached` (not loaded
 * → unloaded); the substring rules cover transitional / error phrasings and
 * other providers. Anything unrecognised is treated as not-loaded.
 */
export function normalizeStatus(raw: string | null | undefined): DataSourceStatus {
  const s = (raw ?? '').toLowerCase()
  if (s === 'attaching' || (s.includes('load') && s.includes('ing')) || s.includes('start')) return 'loading'
  if (s.includes('error') || s.includes('fail')) return 'error'
  if (s === 'attached' || s === 'ready' || s === 'loaded' || s === 'ok' || s === 'connected') return 'ready'
  return 'unloaded'
}

/* ────────────────────────────────────────────────────────────────────────
 * Data sources — reads
 * ──────────────────────────────────────────────────────────────────────── */

export async function listDataSources(): Promise<DataSource[]> {
  return withDemo(
    () =>
      demoDataSources.map((d) => ({
        ...d,
        catalogs: demoLinks
          .filter((l) => l.data_source_name === d.name)
          .map((l) => ({ name: l.catalog_name })),
      })),
    async () => {
      const d = await postGraphQL<{ core: { data_sources: DataSource[] } }>(
        `query {
          core {
            data_sources(order_by: [{ field: "name", direction: ASC }]) {
              name type prefix as_module path description disabled read_only self_defined
              catalogs { name }
            }
          }
        }`,
      )
      return d.core.data_sources
    },
  )
}

/** Runtime status for a single source via `data_source_status(name)`. */
export async function dataSourceStatus(name: string): Promise<DataSourceStatus> {
  return withDemo(
    () => demoStatuses[name] ?? 'unloaded',
    async () => {
      const d = await postGraphQL<{ function: { core: { data_source_status: string } } }>(
        `query ($name: String!) { function { core { data_source_status(name: $name) } } }`,
        { name },
      )
      return normalizeStatus(d.function.core.data_source_status)
    },
  )
}

/** Statuses for many sources → `{ [name]: status }` (N+1 on the real path). */
export async function fetchDataSourceStatuses(
  names: string[],
): Promise<Record<string, DataSourceStatus>> {
  return withDemo(
    () => {
      const out: Record<string, DataSourceStatus> = {}
      for (const n of names) out[n] = demoStatuses[n] ?? 'unloaded'
      return out
    },
    async () => {
      const entries = await Promise.all(
        names.map(async (n) => [n, await dataSourceStatus(n)] as const),
      )
      return Object.fromEntries(entries)
    },
  )
}

function coerceSchemaTypes(raw: unknown): SchemaType[] {
  if (!Array.isArray(raw)) return []
  return raw.map((t): SchemaType => {
    const o = (t ?? {}) as Record<string, unknown>
    const rawFields = o.fields
    let fields = ''
    let count = 0
    if (Array.isArray(rawFields)) {
      count = rawFields.length
      fields = rawFields
        .map((f) => {
          const fo = (f ?? {}) as Record<string, unknown>
          const fname = String(fo.name ?? f ?? '')
          const ftype = fo.type ? ` ${String(fo.type)}` : ''
          return `${fname}${ftype}`.trim()
        })
        .join(' · ')
    } else if (typeof rawFields === 'string') {
      fields = rawFields
      count = rawFields.split('·').length
    }
    return {
      name: String(o.name ?? ''),
      kind: String(o.kind ?? o.type ?? '@table'),
      count: typeof o.count === 'number' ? o.count : count,
      fields,
    }
  })
}

/**
 * Parse the GraphQL SDL text that `describe_data_source_schema` returns into
 * display rows. The engine emits SDL (not JSON), e.g.
 *   type customers @table(name: "customers") {
 *     id: String! @pk @field_source(field: "customer_id")
 *     company_name: String!
 *   }
 * so we split top-level definition blocks and summarise each. Field bodies never
 * nest braces in this dialect, so a non-greedy `{…}` match is safe.
 */
export function parseSchemaSDL(sdl: string): SchemaType[] {
  const out: SchemaType[] = []
  const block = /\b(type|input|interface|enum)\s+([A-Za-z_]\w*)([^{]*)\{([^}]*)\}/g
  let m: RegExpExecArray | null
  while ((m = block.exec(sdl)) !== null) {
    const [, keyword, name, header, body] = m
    // kind = the first directive (@table/@view/@function/…), else the keyword.
    const dir = /@(\w+)/.exec(header)
    const kind = dir ? `@${dir[1]}` : keyword
    const fields = body
      .split('\n')
      .map((l) => l.trim().replace(/\s+@.*$/, '').replace(/,$/, '').trim())
      .filter(Boolean)
    out.push({ name, kind, count: fields.length, fields: fields.join(' · ') })
  }
  return out
}

/** Result of `describe_data_source_schema`: the raw SDL plus parsed type rows. */
export interface SchemaDescription {
  /** The GraphQL SDL text as returned by the engine (for display + download). */
  sdl: string
  /** Parsed type blocks (for the header count / structured view). */
  types: SchemaType[]
}

/** Introspect a source via `describe_data_source_schema(name)`. */
export async function describeDataSourceSchema(name: string): Promise<SchemaDescription> {
  return withDemo(
    () => ({ sdl: DEMO_SDL, types: parseSchemaSDL(DEMO_SDL) }),
    async () => {
      const d = await postGraphQL<{ function: { core: { describe_data_source_schema: unknown } } }>(
        `query ($name: String!) { function { core { describe_data_source_schema(name: $name) } } }`,
        { name },
      )
      const raw = d.function.core.describe_data_source_schema
      if (typeof raw === 'string') {
        // Primary path: the engine returns SDL text.
        const types = parseSchemaSDL(raw)
        if (types.length) return { sdl: raw, types }
        // Fallback: a provider that returns a JSON array/object.
        try {
          return { sdl: raw, types: coerceSchemaTypes(JSON.parse(raw)) }
        } catch {
          return { sdl: raw, types: [] }
        }
      }
      return { sdl: '', types: coerceSchemaTypes(raw) }
    },
  )
}

/* ────────────────────────────────────────────────────────────────────────
 * Data sources — CRUD
 * ──────────────────────────────────────────────────────────────────────── */

export async function insertDataSource(input: DataSourceInput): Promise<FnResult> {
  return withDemo(
    () => {
      demoDataSources.push({
        name: input.name,
        type: input.type,
        prefix: input.prefix ?? '',
        path: input.path ?? '',
        description: input.description ?? '',
        as_module: input.as_module ?? false,
        read_only: input.read_only ?? false,
        self_defined: input.self_defined ?? false,
        disabled: input.disabled ?? false,
      })
      demoStatuses[input.name] = 'unloaded'
      for (const c of input.catalogs ?? []) {
        if (!demoCatalogSources.some((cs) => cs.name === c.name)) {
          demoCatalogSources.push({ name: c.name, type: c.type, path: c.path, description: '' })
        }
        demoLinks.push({ catalog_name: c.name, data_source_name: input.name })
      }
      return ok(`insert_data_sources(data:{name:"${input.name}"}) → created (unloaded)`)
    },
    async () => {
      const d = await postGraphQL<{ core: { insert_data_sources: { name: string } } }>(
        `mutation ($data: core_data_sources_mut_input_data!) {
          core { insert_data_sources(data: $data) { name } }
        }`,
        { data: input },
      )
      return ok(`insert_data_sources → ${d.core.insert_data_sources.name}`)
    },
  )
}

export async function updateDataSource(
  name: string,
  patch: Partial<DataSourceInput>,
): Promise<FnResult> {
  // Nested catalogs only apply on insert.
  const { catalogs: _catalogs, ...data } = patch
  return withDemo(
    () => {
      const i = demoDataSources.findIndex((d) => d.name === name)
      if (i >= 0) demoDataSources[i] = { ...demoDataSources[i], ...data }
      if (data.name && data.name !== name) {
        demoStatuses[data.name] = demoStatuses[name] ?? 'unloaded'
        delete demoStatuses[name]
      }
      return ok(`update_data_sources(filter:{name:{eq:"${name}"}}) → saved`)
    },
    async () => {
      await postGraphQL(
        `mutation ($name: String!, $data: core_data_sources_mut_data!) {
          core { update_data_sources(filter: { name: { eq: $name } }, data: $data) { success message } }
        }`,
        { name, data },
      )
      return ok(`update_data_sources("${name}") → saved`)
    },
  )
}

export async function deleteDataSource(name: string): Promise<FnResult> {
  return withDemo(
    () => {
      const i = demoDataSources.findIndex((d) => d.name === name)
      if (i >= 0) demoDataSources.splice(i, 1)
      delete demoStatuses[name]
      return ok(`delete_data_sources(filter:{name:{eq:"${name}"}}) → deleted`)
    },
    async () => {
      await postGraphQL(
        `mutation ($name: String!) {
          core { delete_data_sources(filter: { name: { eq: $name } }) { success message } }
        }`,
        { name },
      )
      return ok(`delete_data_sources("${name}") → deleted`)
    },
  )
}

/* ────────────────────────────────────────────────────────────────────────
 * Data sources — lifecycle functions
 * ──────────────────────────────────────────────────────────────────────── */

export async function loadDataSource(name: string): Promise<FnResult> {
  return withDemo(
    () => {
      demoStatuses[name] = 'loading'
      // Simulate convergence so the status query (polling while loading) settles.
      setTimeout(() => {
        demoStatuses[name] = 'ready'
      }, 1400)
      return ok(`load_data_source("${name}")`)
    },
    async () => {
      const d = await postGraphQL<{ function: { core: { load_data_source: FnResult } } }>(
        `mutation ($name: String!) {
          function { core { load_data_source(name: $name) { success message } } }
        }`,
        { name },
      )
      return d.function.core.load_data_source
    },
  )
}

export async function unloadDataSource(name: string, hard = false): Promise<FnResult> {
  return withDemo(
    () => {
      demoStatuses[name] = 'unloaded'
      return ok(`unload_data_source("${name}", hard: ${hard})`)
    },
    async () => {
      const d = await postGraphQL<{ function: { core: { unload_data_source: FnResult } } }>(
        `mutation ($name: String!, $hard: Boolean!) {
          function { core { unload_data_source(name: $name, hard: $hard) { success message } } }
        }`,
        { name, hard },
      )
      return d.function.core.unload_data_source
    },
  )
}

export async function checkpointDataSource(name: string): Promise<FnResult> {
  return withDemo(
    () => ok(`checkpoint("${name}") → success`),
    async () => {
      const d = await postGraphQL<{ function: { core: { checkpoint: FnResult } } }>(
        `mutation ($name: String!) {
          function { core { checkpoint(name: $name) { success message } } }
        }`,
        { name },
      )
      return d.function.core.checkpoint
    },
  )
}

/* ────────────────────────────────────────────────────────────────────────
 * Catalogs — reads
 * ──────────────────────────────────────────────────────────────────────── */

export async function listCatalogSources(): Promise<CatalogSource[]> {
  return withDemo(
    () => demoCatalogSources.map((c) => ({ ...c })),
    async () => {
      const d = await postGraphQL<{ core: { catalog_sources: CatalogSource[] } }>(
        `query {
          core {
            catalog_sources(order_by: [{ field: "name", direction: ASC }]) {
              name type description path
            }
          }
        }`,
      )
      return d.core.catalog_sources
    },
  )
}

export async function listCatalogLinks(): Promise<CatalogLink[]> {
  return withDemo(
    () => demoLinks.map((l) => ({ ...l })),
    async () => {
      const d = await postGraphQL<{ core: { catalogs: CatalogLink[] } }>(
        `query { core { catalogs { catalog_name data_source_name } } }`,
      )
      return d.core.catalogs
    },
  )
}

/* ────────────────────────────────────────────────────────────────────────
 * Catalogs — CRUD + links
 * ──────────────────────────────────────────────────────────────────────── */

export async function insertCatalogSource(input: CatalogSourceInput): Promise<FnResult> {
  return withDemo(
    () => {
      demoCatalogSources.push({
        name: input.name,
        type: input.type ?? 'localFS',
        description: input.description ?? '',
        path: input.path ?? '',
      })
      return ok(`insert_catalog_sources(data:{name:"${input.name}"}) → created`)
    },
    async () => {
      const d = await postGraphQL<{ core: { insert_catalog_sources: { name: string } } }>(
        `mutation ($data: core_catalog_sources_mut_input_data!) {
          core { insert_catalog_sources(data: $data) { name } }
        }`,
        { data: input },
      )
      return ok(`insert_catalog_sources → ${d.core.insert_catalog_sources.name}`)
    },
  )
}

export async function updateCatalogSource(
  name: string,
  patch: Partial<CatalogSourceInput>,
): Promise<FnResult> {
  return withDemo(
    () => {
      const i = demoCatalogSources.findIndex((c) => c.name === name)
      if (i >= 0) demoCatalogSources[i] = { ...demoCatalogSources[i], ...patch }
      return ok(`update_catalog_sources("${name}") → saved`)
    },
    async () => {
      await postGraphQL(
        `mutation ($name: String!, $data: core_catalog_sources_mut_data!) {
          core { update_catalog_sources(filter: { name: { eq: $name } }, data: $data) { success message } }
        }`,
        { name, data: patch },
      )
      return ok(`update_catalog_sources("${name}") → saved`)
    },
  )
}

export async function deleteCatalogSource(name: string): Promise<FnResult> {
  return withDemo(
    () => {
      const i = demoCatalogSources.findIndex((c) => c.name === name)
      if (i >= 0) demoCatalogSources.splice(i, 1)
      for (let j = demoLinks.length - 1; j >= 0; j--) {
        if (demoLinks[j].catalog_name === name) demoLinks.splice(j, 1)
      }
      return ok(`delete_catalog_sources(filter:{name:{eq:"${name}"}}) → deleted`)
    },
    async () => {
      await postGraphQL(
        `mutation ($name: String!) {
          core { delete_catalog_sources(filter: { name: { eq: $name } }) { success message } }
        }`,
        { name },
      )
      return ok(`delete_catalog_sources("${name}") → deleted`)
    },
  )
}

export async function linkCatalog(
  catalog_name: string,
  data_source_name: string,
): Promise<FnResult> {
  return withDemo(
    () => {
      if (!demoLinks.some((l) => l.catalog_name === catalog_name && l.data_source_name === data_source_name)) {
        demoLinks.push({ catalog_name, data_source_name })
      }
      return ok(`insert_catalogs(catalog_name:"${catalog_name}", data_source_name:"${data_source_name}")`)
    },
    async () => {
      await postGraphQL(
        `mutation ($data: core_catalogs_mut_input_data!) {
          core { insert_catalogs(data: $data) { success message } }
        }`,
        { data: { catalog_name, data_source_name } },
      )
      return ok(`insert_catalogs("${catalog_name}" ⇄ "${data_source_name}")`)
    },
  )
}

export async function unlinkCatalog(
  catalog_name: string,
  data_source_name: string,
): Promise<FnResult> {
  return withDemo(
    () => {
      const i = demoLinks.findIndex(
        (l) => l.catalog_name === catalog_name && l.data_source_name === data_source_name,
      )
      if (i >= 0) demoLinks.splice(i, 1)
      return ok(`delete_catalogs(catalog_name:"${catalog_name}", data_source_name:"${data_source_name}")`)
    },
    async () => {
      await postGraphQL(
        `mutation ($cat: String!, $ds: String!) {
          core {
            delete_catalogs(
              filter: { catalog_name: { eq: $cat }, data_source_name: { eq: $ds } }
            ) { success message }
          }
        }`,
        { cat: catalog_name, ds: data_source_name },
      )
      return ok(`delete_catalogs("${catalog_name}" ⇄ "${data_source_name}")`)
    },
  )
}

/* ────────────────────────────────────────────────────────────────────────
 * Schema maintenance functions (per catalog source)
 * ──────────────────────────────────────────────────────────────────────── */

export async function schemaVersionClean(name: string): Promise<FnResult> {
  return withDemo(
    () => ok(`_schema_version_clean("${name}") → stale versions removed`),
    async () => {
      const d = await postGraphQL<{ function: { core: { _schema_version_clean: FnResult } } }>(
        `mutation ($name: String!) {
          function { core { _schema_version_clean(name: $name) { success message } } }
        }`,
        { name },
      )
      return d.function.core._schema_version_clean
    },
  )
}
