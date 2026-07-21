import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'
import type { FnResult } from './platform-sources'

/* ────────────────────────────────────────────────────────────────────────
 * DuckLake management — reads (core.ducklake.*) + maintenance / options
 * (function.core.ducklake.*). Every op is keyed by the ducklake data-source
 * `name`. Verified live against the `taxi` source; see platform-sources for
 * the shared FnResult envelope.
 * ──────────────────────────────────────────────────────────────────────── */

/** A row of `core.ducklake.table_stats` — flat (the engine exposes no schema). */
export interface DuckLakeTableStat {
  table_name: string
  table_id: number
  table_uuid: string
  file_count: number
  file_size_bytes: number
  delete_file_count: number
  delete_file_size_bytes: number
}

/** A row of `core.ducklake.snapshots` — the time-travel history. */
export interface DuckLakeSnapshot {
  snapshot_id: number
  snapshot_time: string
  schema_version: number
  /** JSON string, e.g. `{"tables_created":["main.trips"]}`. */
  changes: string
}

/** A row of `core.ducklake.data_files` for one table. */
export interface DuckLakeDataFile {
  data_file: string
  data_file_size_bytes: number
  delete_file: string | null
  delete_file_size_bytes: number | null
}

/**
 * Tunable options for `set_option`. Authoritative enum `core_ducklake_Option`
 * from the engine; kept explicit so the UI can offer a picker (the value is a
 * free String the engine validates per option).
 */
export const DUCKLAKE_OPTIONS = [
  'target_file_size',
  'auto_compact',
  'expire_older_than',
  'delete_older_than',
  'rewrite_delete_threshold',
  'data_inlining_row_limit',
  'parquet_compression',
  'parquet_compression_level',
  'parquet_version',
  'parquet_row_group_size',
  'parquet_row_group_size_bytes',
  'hive_file_pattern',
  'require_commit_message',
  'encrypted',
  'per_thread_output',
  'data_path',
] as const

export type DuckLakeOption = (typeof DUCKLAKE_OPTIONS)[number]

/* ── demo backing store ─────────────────────────────────────────────────── */

const demoTables: DuckLakeTableStat[] = [
  { table_name: 'trips', table_id: 2, table_uuid: '019cd8d0-c5be-7b9c', file_count: 12, file_size_bytes: 844642102, delete_file_count: 1, delete_file_size_bytes: 20480 },
  { table_name: 'zones', table_id: 1, table_uuid: '019cd8d0-c5a1-7081', file_count: 1, file_size_bytes: 5436, delete_file_count: 0, delete_file_size_bytes: 0 },
  { table_name: 'daily_summary', table_id: 3, table_uuid: '019cd8d6-6991-7186', file_count: 0, file_size_bytes: 0, delete_file_count: 0, delete_file_size_bytes: 0 },
]

const demoSnapshots: DuckLakeSnapshot[] = [
  { snapshot_id: 4, snapshot_time: '2026-03-10T18:35:02.114000+01:00', schema_version: 2, changes: '{"tables_inserted_into":["2"]}' },
  { snapshot_id: 3, snapshot_time: '2026-03-10T18:34:46.716000+01:00', schema_version: 2, changes: '{"tables_created":["main.trips"]}' },
  { snapshot_id: 2, snapshot_time: '2026-03-10T18:34:46.690000+01:00', schema_version: 1, changes: '{"tables_inserted_into":["1"]}' },
  { snapshot_id: 1, snapshot_time: '2026-03-10T18:34:46.680000+01:00', schema_version: 1, changes: '{"tables_created":["main.zones"]}' },
  { snapshot_id: 0, snapshot_time: '2026-03-10T18:34:46.612000+01:00', schema_version: 0, changes: '{"schemas_created":["main"]}' },
]

const demoFiles: Record<string, DuckLakeDataFile[]> = {
  trips: [
    { data_file: 's3://ducklake-taxi/data/main/trips/ducklake-019cd8d6-28bf.parquet', data_file_size_bytes: 302621335, delete_file: null, delete_file_size_bytes: null },
    { data_file: 's3://ducklake-taxi/data/main/trips/ducklake-019cd8d6-28be.parquet', data_file_size_bytes: 542020767, delete_file: 's3://ducklake-taxi/data/main/trips/ducklake-del-01.parquet', delete_file_size_bytes: 20480 },
  ],
  zones: [
    { data_file: 's3://ducklake-taxi/data/main/zones/ducklake-019cd8d0-c5a1.parquet', data_file_size_bytes: 5436, delete_file: null, delete_file_size_bytes: null },
  ],
  daily_summary: [],
}

const ok = (message: string): FnResult => ({ success: true, message })

/* ── reads ──────────────────────────────────────────────────────────────── */

export async function fetchTableStats(name: string): Promise<DuckLakeTableStat[]> {
  return withDemo(
    () => demoTables.map((t) => ({ ...t })),
    async () => {
      const d = await postGraphQL<{ core: { ducklake: { table_stats: DuckLakeTableStat[] } } }>(
        `query ($name: String!) {
          core { ducklake { table_stats(args: { name: $name }) {
            table_name table_id table_uuid file_count file_size_bytes
            delete_file_count delete_file_size_bytes
          } } }
        }`,
        { name },
      )
      return d.core.ducklake.table_stats
    },
  )
}

export async function fetchSnapshots(
  name: string,
  limit = 50,
  offset = 0,
): Promise<DuckLakeSnapshot[]> {
  return withDemo(
    () => demoSnapshots.slice(offset, offset + limit).map((s) => ({ ...s })),
    async () => {
      // order_by is a `[OrderByField]` list whose `direction` is an enum — the
      // engine only accepts it inline (a variable trips "expected object"), so
      // sort newest-first here and page with limit/offset variables.
      const d = await postGraphQL<{ core: { ducklake: { snapshots: DuckLakeSnapshot[] } } }>(
        `query ($name: String!, $limit: Int!, $offset: Int!) {
          core { ducklake { snapshots(
            args: { name: $name }
            order_by: [{ field: "snapshot_id", direction: DESC }]
            limit: $limit
            offset: $offset
          ) {
            snapshot_id snapshot_time schema_version changes
          } } }
        }`,
        { name, limit, offset },
      )
      return d.core.ducklake.snapshots
    },
  )
}

export async function fetchDataFiles(
  name: string,
  table_name: string,
  schema_name?: string,
): Promise<DuckLakeDataFile[]> {
  return withDemo(
    () => (demoFiles[table_name] ?? []).map((f) => ({ ...f })),
    async () => {
      const d = await postGraphQL<{ core: { ducklake: { data_files: DuckLakeDataFile[] } } }>(
        `query ($name: String!, $table: String!, $schema: String) {
          core { ducklake { data_files(args: { name: $name, table_name: $table, schema_name: $schema }) {
            data_file data_file_size_bytes delete_file delete_file_size_bytes
          } } }
        }`,
        { name, table: table_name, schema: schema_name ?? null },
      )
      return d.core.ducklake.data_files
    },
  )
}

/* ── maintenance ──────────────────────────────────────────────────────────
 * All return the shared `{ success, message }` envelope. Destructive ops
 * (expire_snapshots, cleanup_old_files) take `dry_run`; callers default it on.
 * ──────────────────────────────────────────────────────────────────────── */

export function checkpoint(name: string): Promise<FnResult> {
  return ducklakeVars('checkpoint', { name: 'String!' }, { name }, `checkpoint("${name}")`)
}

export function flushInlinedData(name: string): Promise<FnResult> {
  return ducklakeVars('flush_inlined_data', { name: 'String!' }, { name }, `flush_inlined_data("${name}")`)
}

export function expireSnapshots(
  name: string,
  opts: { versions?: string; older_than?: string; dry_run: boolean },
): Promise<FnResult> {
  return ducklakeVars(
    'expire_snapshots',
    { name: 'String!', versions: 'String', older_than: 'String', dry_run: 'Boolean' },
    { name, versions: opts.versions ?? null, older_than: opts.older_than ?? null, dry_run: opts.dry_run },
    `expire_snapshots("${name}", dry_run: ${opts.dry_run})`,
  )
}

export function cleanupOldFiles(
  name: string,
  opts: { older_than?: string; cleanup_all?: boolean; dry_run: boolean },
): Promise<FnResult> {
  return ducklakeVars(
    'cleanup_old_files',
    { name: 'String!', older_than: 'String', cleanup_all: 'Boolean', dry_run: 'Boolean' },
    { name, older_than: opts.older_than ?? null, cleanup_all: opts.cleanup_all ?? null, dry_run: opts.dry_run },
    `cleanup_old_files("${name}", dry_run: ${opts.dry_run})`,
  )
}

export function mergeAdjacentFiles(
  name: string,
  opts: { schema_name?: string; table_name?: string } = {},
): Promise<FnResult> {
  return ducklakeVars(
    'merge_adjacent_files',
    { name: 'String!', schema_name: 'String', table_name: 'String' },
    { name, schema_name: opts.schema_name ?? null, table_name: opts.table_name ?? null },
    `merge_adjacent_files("${name}")`,
  )
}

export function rewriteDataFiles(
  name: string,
  opts: { schema_name?: string; table_name?: string; delete_threshold?: number } = {},
): Promise<FnResult> {
  return ducklakeVars(
    'rewrite_data_files',
    { name: 'String!', schema_name: 'String', table_name: 'String', delete_threshold: 'Float' },
    {
      name,
      schema_name: opts.schema_name ?? null,
      table_name: opts.table_name ?? null,
      delete_threshold: opts.delete_threshold ?? null,
    },
    `rewrite_data_files("${name}")`,
  )
}

export function setOption(
  name: string,
  option: DuckLakeOption,
  value: string,
  scope: { schema_name?: string; table_name?: string } = {},
): Promise<FnResult> {
  return withDemo(
    () => ok(`set_option("${name}", ${option}: ${value})`),
    async () => {
      // The `option` enum must be inlined — the engine rejects an enum passed via
      // a GraphQL variable ("expected object"). `option` comes from the fixed
      // DUCKLAKE_OPTIONS allowlist, so inlining the literal is safe. `value` is a
      // required String!; schema/table are optional.
      const decl = ['$name: String!', '$value: String!']
      const args = ['name: $name', `option: ${option}`, 'value: $value']
      const vars: Record<string, unknown> = { name, value }
      if (scope.schema_name) {
        decl.push('$schema_name: String')
        args.push('schema_name: $schema_name')
        vars.schema_name = scope.schema_name
      }
      if (scope.table_name) {
        decl.push('$table_name: String')
        args.push('table_name: $table_name')
        vars.table_name = scope.table_name
      }
      const d = await postGraphQL<{ function: { core: { ducklake: { set_option: FnResult } } } }>(
        `mutation (${decl.join(', ')}) { function { core { ducklake { set_option(${args.join(', ')}) { success message } } } } }`,
        vars,
      )
      return d.function.core.ducklake.set_option
    },
  )
}

/**
 * Run a `function.core.ducklake.<op>` mutation with typed variables. The op
 * signatures are heterogeneous, so callers pass the GraphQL var types + values
 * explicitly. Only args with a non-null/undefined value are declared and sent —
 * the engine rejects an explicit `null` for an optional arg ("required
 * arguments"), whereas omitting it lets the op use its default (e.g.
 * `expire_snapshots` with just `name` + `dry_run`).
 */
function ducklakeVars(
  op: string,
  varTypes: Record<string, string>,
  values: Record<string, unknown>,
  demoMsg: string,
): Promise<FnResult> {
  return withDemo(
    () => ok(demoMsg),
    async () => {
      const keys = Object.keys(varTypes).filter((k) => values[k] !== null && values[k] !== undefined)
      const decl = keys.map((k) => `$${k}: ${varTypes[k]}`).join(', ')
      const call = keys.map((k) => `${k}: $${k}`).join(', ')
      const vars = Object.fromEntries(keys.map((k) => [k, values[k]]))
      const d = await postGraphQL<{ function: { core: { ducklake: Record<string, FnResult> } } }>(
        `mutation (${decl}) { function { core { ducklake { ${op}(${call}) { success message } } } } }`,
        vars,
      )
      return d.function.core.ducklake[op]
    },
  )
}
