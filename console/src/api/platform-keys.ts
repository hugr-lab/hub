import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/**
 * `core.api_keys` row — service-to-service auth carrying a `default_role`.
 * `headers` / `claims` are JSON columns carried as raw JSON text in the UI.
 * The secret `key` is only ever returned once, at creation.
 */
export interface ApiKey {
  name: string
  description: string
  default_role: string
  disabled: boolean
  is_temporal: boolean
  expires_at: string | null
  headers: string
  claims: string
}

/** Fields the create form collects. */
export interface NewApiKey {
  name: string
  description: string
  default_role: string
  is_temporal: boolean
  expires_at: string | null
  headers: string
  claims: string
}

/** Result of creating a key — the row plus the one-time revealed secret. */
export interface ApiKeyCreated {
  row: ApiKey
  key: string
}

// ---------------------------------------------------------------------------
// Demo store (mutable so the offline `?demo=1` screen stays interactive)
// ---------------------------------------------------------------------------

const MOCK_KEYS: ApiKey[] = [
  {
    name: 'grafana-reader',
    description: 'BI dashboards',
    default_role: 'viewer',
    disabled: false,
    is_temporal: false,
    expires_at: null,
    headers: '',
    claims: '',
  },
  {
    name: 'etl-nightly',
    description: 'Airflow DAGs',
    default_role: 'service-etl',
    disabled: false,
    is_temporal: true,
    expires_at: '2026-09-30',
    headers: '',
    claims: '{ "team": "data-platform" }',
  },
  {
    name: 'superset-reader',
    description: 'Superset embedded dashboards',
    default_role: 'viewer',
    disabled: false,
    is_temporal: false,
    expires_at: null,
    headers: '{ "X-Tenant": "acme" }',
    claims: '',
  },
  {
    name: 'legacy-export',
    description: 'Deprecated 2026-06',
    default_role: 'analyst',
    disabled: true,
    is_temporal: false,
    expires_at: null,
    headers: '',
    claims: '',
  },
]

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T

const textToJson = (text: string): unknown => {
  const trimmed = text.trim()
  if (!trimmed) return null
  try {
    return JSON.parse(trimmed)
  } catch {
    return null
  }
}

const jsonToText = (v: unknown): string =>
  v == null ? '' : typeof v === 'string' ? v : JSON.stringify(v, null, 2)

interface RawApiKey extends Omit<ApiKey, 'headers' | 'claims'> {
  headers: unknown
  claims: unknown
}

const rawToKey = (r: RawApiKey): ApiKey => ({
  name: r.name,
  description: r.description,
  default_role: r.default_role,
  disabled: r.disabled,
  is_temporal: r.is_temporal,
  expires_at: r.expires_at,
  headers: jsonToText(r.headers),
  claims: jsonToText(r.claims),
})

const genKey = () =>
  'hga_' +
  Math.random().toString(36).slice(2, 12) +
  Math.random().toString(36).slice(2, 12)

// hugr-generated input type names (verified against the live schema): insert →
// `core_<table>_mut_input_data` (returns the row), update → `core_<table>_mut_data`
// (returns OperationResult), filter → `core_<table>_filter` (NOT `_filter_input`).
// ---------------------------------------------------------------------------
// Fetchers + mutations
// ---------------------------------------------------------------------------

export async function listApiKeys(): Promise<ApiKey[]> {
  return withDemo(
    () => clone(MOCK_KEYS).sort((a, b) => a.name.localeCompare(b.name)),
    async () => {
      const d = await postGraphQL<{ core: { api_keys: RawApiKey[] } }>(
        `query { core { api_keys { name description default_role disabled is_temporal expires_at headers claims } } }`,
      )
      return d.core.api_keys.map(rawToKey).sort((a, b) => a.name.localeCompare(b.name))
    },
  )
}

export async function insertApiKey(input: NewApiKey): Promise<ApiKeyCreated> {
  const row: ApiKey = {
    name: input.name,
    description: input.description,
    default_role: input.default_role,
    disabled: false,
    is_temporal: input.is_temporal || !!input.expires_at,
    expires_at: input.expires_at,
    headers: input.headers,
    claims: input.claims,
  }
  return withDemo(
    () => {
      MOCK_KEYS.push(clone(row))
      return { row, key: genKey() }
    },
    async () => {
      const d = await postGraphQL<{ core: { insert_api_keys: { name: string; key: string } } }>(
        `mutation($data: core_api_keys_mut_input_data!){ core { insert_api_keys(data: $data){ name key } } }`,
        {
          data: {
            name: row.name,
            description: row.description,
            default_role: row.default_role,
            is_temporal: row.is_temporal,
            expires_at: row.expires_at,
            headers: textToJson(row.headers),
            claims: textToJson(row.claims),
          },
        },
      )
      return { row, key: d.core.insert_api_keys.key }
    },
  )
}

export async function updateApiKey(name: string, patch: Partial<Omit<ApiKey, 'name'>>): Promise<void> {
  return withDemo(
    () => {
      const i = MOCK_KEYS.findIndex((k) => k.name === name)
      if (i >= 0) MOCK_KEYS[i] = { ...MOCK_KEYS[i], ...patch }
    },
    async () => {
      const data: Record<string, unknown> = { ...patch }
      if ('headers' in patch) data.headers = textToJson(patch.headers as string)
      if ('claims' in patch) data.claims = textToJson(patch.claims as string)
      await postGraphQL(
        `mutation($filter: core_api_keys_filter!, $data: core_api_keys_mut_data!){ core { update_api_keys(filter: $filter, data: $data){ success message } } }`,
        { filter: { name: { eq: name } }, data },
      )
    },
  )
}

/** Enable / disable a key (thin wrapper over `update_api_keys`). */
export async function setApiKeyDisabled(name: string, disabled: boolean): Promise<void> {
  return updateApiKey(name, { disabled })
}

export async function deleteApiKey(name: string): Promise<void> {
  return withDemo(
    () => {
      const i = MOCK_KEYS.findIndex((k) => k.name === name)
      if (i >= 0) MOCK_KEYS.splice(i, 1)
    },
    async () => {
      await postGraphQL(
        `mutation($filter: core_api_keys_filter!){ core { delete_api_keys(filter: $filter){ success message } } }`,
        { filter: { name: { eq: name } } },
      )
    },
  )
}
