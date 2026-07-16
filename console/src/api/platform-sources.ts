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

/** Row of `core.catalog_sources`. */
export interface CatalogSource {
  name: string
  type: 'localFS' | 'uri' | string
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

const DEMO_SCHEMA: SchemaType[] = [
  { name: 'gateways', kind: '@table', count: 9, fields: 'id ID! · site String · last_seen Timestamp · status String · location Geometry' },
  { name: 'readings', kind: '@table', count: 6, fields: 'gateway_id → gateways.id · metric String · value Float · at Timestamp' },
  { name: 'daily_rollup', kind: '@view', count: 5, fields: 'day Date · site String · avg_value Float · sample_count Int' },
  { name: 'sites_h3', kind: '@function', count: 3, fields: 'h3_cell(resolution Int) → String · geometry aggregations' },
]

const ok = (message: string): FnResult => ({ success: true, message })

/* ────────────────────────────────────────────────────────────────────────
 * Status helpers
 * ──────────────────────────────────────────────────────────────────────── */

/** Map an arbitrary `data_source_status` string onto a UI status. */
export function normalizeStatus(raw: string | null | undefined): DataSourceStatus {
  const s = (raw ?? '').toLowerCase()
  if (s.includes('load') && s.includes('ing')) return 'loading'
  if (s.includes('start')) return 'loading'
  if (s.includes('error') || s.includes('fail')) return 'error'
  if (s === 'ready' || s === 'loaded' || s === 'ok' || s === 'connected') return 'ready'
  if (s === '' || s.includes('unload') || s.includes('not') || s.includes('disabled')) return 'unloaded'
  return 'unloaded'
}

/* ────────────────────────────────────────────────────────────────────────
 * Data sources — reads
 * ──────────────────────────────────────────────────────────────────────── */

export async function listDataSources(): Promise<DataSource[]> {
  return withDemo(
    () => demoDataSources.map((d) => ({ ...d })),
    async () => {
      const d = await postGraphQL<{ core: { data_sources: DataSource[] } }>(
        `query {
          core {
            data_sources(order_by: { field: "name", direction: ASC }) {
              name type prefix as_module path description disabled read_only self_defined
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

/** Introspect a source via `describe_data_source_schema(name)`. */
export async function describeDataSourceSchema(name: string): Promise<SchemaType[]> {
  return withDemo(
    () => DEMO_SCHEMA.map((t) => ({ ...t })),
    async () => {
      // Return shape is deployment-defined; coerce defensively (see TODO in task notes).
      const d = await postGraphQL<{ function: { core: { describe_data_source_schema: unknown } } }>(
        `query ($name: String!) { function { core { describe_data_source_schema(name: $name) } } }`,
        { name },
      )
      return coerceSchemaTypes(d.function.core.describe_data_source_schema)
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
        disabled: false,
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
        `mutation ($data: data_sources_mut_input_data!) {
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
        `mutation ($name: String!, $data: data_sources_mut_input_data!) {
          core { update_data_sources(filter: { name: { eq: $name } }, data: $data) { name } }
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
          core { delete_data_sources(filter: { name: { eq: $name } }) { name } }
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
            catalog_sources(order_by: { field: "name", direction: ASC }) {
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
        `mutation ($data: catalog_sources_mut_input_data!) {
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
        `mutation ($name: String!, $data: catalog_sources_mut_input_data!) {
          core { update_catalog_sources(filter: { name: { eq: $name } }, data: $data) { name } }
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
          core { delete_catalog_sources(filter: { name: { eq: $name } }) { name } }
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
        `mutation ($data: catalogs_mut_input_data!) {
          core { insert_catalogs(data: $data) { catalog_name data_source_name } }
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
            ) { catalog_name }
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

export async function schemaReindex(name: string, batchSize = 500): Promise<FnResult> {
  return withDemo(
    () => ok(`_schema_reindex("${name}", batch_size: ${batchSize}) → queued`),
    async () => {
      const d = await postGraphQL<{ function: { core: { _schema_reindex: FnResult } } }>(
        `mutation ($name: String!, $batch: Int!) {
          function { core { _schema_reindex(name: $name, batch_size: $batch) { success message } } }
        }`,
        { name, batch: batchSize },
      )
      return d.function.core._schema_reindex
    },
  )
}

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

export async function schemaHardRemove(name: string): Promise<FnResult> {
  return withDemo(
    () => ok(`_schema_hard_remove("${name}") → removed`),
    async () => {
      const d = await postGraphQL<{ function: { core: { _schema_hard_remove: FnResult } } }>(
        `mutation ($name: String!) {
          function { core { _schema_hard_remove(name: $name) { success message } } }
        }`,
        { name },
      )
      return d.function.core._schema_hard_remove
    },
  )
}
