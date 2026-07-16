import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'
import { normalizeStatus } from './platform-sources'

/**
 * Monitoring / dashboard data layer. Every fetcher is self-contained and wraps
 * `withDemo` so the screen is fully interactive offline (`/console/?demo=1`).
 * Demo seeds mirror the design prototype so demo mode looks identical.
 *
 * Backing calls: `hub.my_agent_instances` (fleet), `core.data_sources` +
 * `function.core.data_source_status(name)` (source health), `hub.db.llm_budgets`
 * (budgets). Stat tiles are derived aggregates; recent activity is a mocked
 * audit/session-event feed (no dedicated table in the contract yet).
 */

/* ── Stat tiles ─────────────────────────────────────────────── */

export interface StatTileData {
  label: string
  value: string
  sub: string
  /** CSS-var color token for the sub-note (fed to the kit `StatTile.subColor`). */
  subColor: string
}

// Exact values from the prototype render model (design-prototype.dc.html
// `statTiles`): 2/5 agents running (the seed has two `running` agents, matching
// the fleet rollup below), 4/6 data sources ready.
const MOCK_STATS: StatTileData[] = [
  { label: 'Agents running', value: '2 / 5', sub: '1 starting', subColor: 'var(--amber)' },
  { label: 'Active sessions', value: '4', sub: '2 waiting on tools', subColor: 'var(--text3)' },
  { label: 'Tokens · 24h', value: '1.28M', sub: '+12% vs prior day', subColor: 'var(--green)' },
  { label: 'Data sources ready', value: '4 / 6', sub: '1 error · 1 loading', subColor: 'var(--red)' },
]

export async function getMonitoringStats(): Promise<StatTileData[]> {
  return withDemo(MOCK_STATS, async () => {
    // Derive the count-based tiles from the real fleet + data-source health.
    // Session-count and 24h token aggregates lack a contract endpoint → left as
    // best-effort placeholders until a usage-aggregate API exists.
    const [fleet, ds] = await Promise.all([getFleetRollup(), getDataSourceHealth()])
    const running = fleet.filter((a) => a.runtime === 'running').length
    const starting = fleet.filter((a) => a.runtime === 'starting').length
    const ready = ds.filter((d) => d.status === 'ready').length
    const errored = ds.filter((d) => d.status === 'error').length
    const loading = ds.filter((d) => d.status === 'loading').length
    const sessions = fleet.reduce((n, a) => n + a.sessions, 0)
    const dsSub =
      [errored ? `${errored} error` : '', loading ? `${loading} loading` : '']
        .filter(Boolean)
        .join(' · ') || 'all healthy'
    return [
      {
        label: 'Agents running',
        value: `${running} / ${fleet.length}`,
        sub: starting ? `${starting} starting` : 'steady',
        subColor: 'var(--amber)',
      },
      { label: 'Active sessions', value: String(sessions), sub: '', subColor: 'var(--text3)' },
      { label: 'Tokens · 24h', value: '—', sub: 'usage aggregate pending', subColor: 'var(--text3)' },
      {
        label: 'Data sources ready',
        value: `${ready} / ${ds.length}`,
        sub: dsSub,
        subColor: 'var(--red)',
      },
    ]
  })
}

/* ── Data source health rollup ──────────────────────────────── */

export interface DataSourceHealth {
  name: string
  type: string
  path: string
  /** Live status string, e.g. `ready` / `loading` / `error` / `disabled`. */
  status: string
}

const MOCK_DS_HEALTH: DataSourceHealth[] = [
  { name: 'core-warehouse', type: 'postgres', path: 'postgres://hub@pg-core:5432/warehouse', status: 'ready' },
  { name: 'iot-lake', type: 'ducklake', path: 's3://hugr-lake/iot', status: 'ready' },
  { name: 'billing', type: 'mysql', path: 'mysql://hub@my-bill:3306/billing', status: 'error' },
  { name: 'geo-files', type: 'duckdb', path: '/data/geo/geo.db', status: 'ready' },
  { name: 'weather-api', type: 'http', path: 'https://api.meteo.internal/v2', status: 'loading' },
  { name: 'claude', type: 'llm-anthropic', path: 'https://api.anthropic.com', status: 'ready' },
]

interface RawDataSource {
  name: string
  type: string
  path: string
  disabled: boolean
}

export async function getDataSourceHealth(): Promise<DataSourceHealth[]> {
  return withDemo(MOCK_DS_HEALTH, async () => {
    const d = await postGraphQL<{ core: { data_sources: RawDataSource[] } }>(
      `query { core { data_sources { name type path disabled } } }`,
    )
    const rows = d.core.data_sources
    // Live status comes from the per-source status function; degrade to the
    // `disabled` column if that call is unavailable. Normalize the engine's
    // raw vocabulary (attached/detached/…) so the count + dots are correct.
    const statuses = await fetchDataSourceStatuses(rows.map((r) => r.name)).catch(
      () => ({}) as Record<string, string>,
    )
    return rows.map((r) => ({
      name: r.name,
      type: r.type,
      path: r.path,
      status: normalizeStatus(statuses[r.name] ?? (r.disabled ? 'disabled' : '')),
    }))
  })
}

/** Batch `function.core.data_source_status(name)` into one aliased read. */
async function fetchDataSourceStatuses(names: string[]): Promise<Record<string, string>> {
  if (names.length === 0) return {}
  const selection = names.map((n, i) => `s${i}: data_source_status(name:${JSON.stringify(n)})`).join(' ')
  const d = await postGraphQL<{ function: { core: Record<string, string> } }>(
    `query { function { core { ${selection} } } }`,
  )
  const core = d.function.core
  const out: Record<string, string> = {}
  names.forEach((n, i) => {
    out[n] = core[`s${i}`]
  })
  return out
}

/* ── Fleet rollup ───────────────────────────────────────────── */

export interface FleetRow {
  id: string
  name: string
  /** Live session count (0 hides the "N sess" chip). */
  sessions: number
  /** Runtime state, e.g. `running` / `starting` / `stopped`. */
  runtime: string
}

const MOCK_FLEET: FleetRow[] = [
  { id: 'agt_7f3a', name: 'analytics-copilot', sessions: 3, runtime: 'running' },
  { id: 'agt_2c91', name: 'geo-research', sessions: 1, runtime: 'running' },
  { id: 'agt_9d10', name: 'etl-warden', sessions: 0, runtime: 'stopped' },
  { id: 'agt_4b77', name: 'finance-qa', sessions: 0, runtime: 'starting' },
  { id: 'agt_0e42', name: 'support-triage', sessions: 0, runtime: 'stopped' },
]

interface RawAgentInstance {
  id: string
  display_name: string
  status: string
}

interface SessionStatusBucket {
  key: { agent_id?: string; status: string }
  aggregations: { _rows_count: number }
}

/**
 * Active sessions per agent, from the `hub.agent.db.sessions` bucket
 * aggregation. Best-effort: if the caller's role can't read the agent session
 * store, returns `{}` so the fleet still renders (session chips read 0).
 */
async function fetchActiveSessionsByAgent(): Promise<Record<string, number>> {
  try {
    const d = await postGraphQL<{
      hub: { agent: { db: { sessions_bucket_aggregation: SessionStatusBucket[] } } }
    }>(
      `query {
        hub { agent { db {
          sessions_bucket_aggregation { key { agent_id status } aggregations { _rows_count } }
        } } }
      }`,
    )
    const out: Record<string, number> = {}
    for (const b of d.hub.agent.db.sessions_bucket_aggregation) {
      if (b.key.status === 'active' && b.key.agent_id) {
        out[b.key.agent_id] = (out[b.key.agent_id] ?? 0) + b.aggregations._rows_count
      }
    }
    return out
  } catch {
    return {}
  }
}

export async function getFleetRollup(): Promise<FleetRow[]> {
  return withDemo(MOCK_FLEET, async () => {
    const [d, sessionsByAgent] = await Promise.all([
      postGraphQL<{ hub: { my_agent_instances: RawAgentInstance[] } }>(
        `query { hub { my_agent_instances { id display_name status } } }`,
      ),
      fetchActiveSessionsByAgent(),
    ])
    return d.hub.my_agent_instances.map((a) => ({
      id: a.id,
      name: a.display_name,
      sessions: sessionsByAgent[a.id] ?? 0,
      runtime: a.status,
    }))
  })
}

/* ── LLM budgets ────────────────────────────────────────────── */

export interface LlmBudget {
  name: string
  used: string
  limit: string
  /** Percent of the budget consumed (0..100). */
  pct: number
}

const MOCK_BUDGETS: LlmBudget[] = [
  { name: 'analytics-q3', used: '$412', limit: '$600', pct: 68 },
  { name: 'finance-q3', used: '$188', limit: '$200', pct: 94 },
  { name: 'geo-research', used: '$61', limit: '$300', pct: 20 },
]

interface RawBudget {
  id: string
  scope: string
  provider_id: string | null
  period: string
  max_tokens_in: number | null
  max_tokens_out: number | null
  max_requests: number | null
}

/**
 * `hub.db.llm_budgets` is a budget-CONFIG table (token/request LIMITS per
 * scope/period) — there is no consumption column, and no usage-aggregate table
 * in the contract yet. So `used`/`pct` degrade to placeholders until a usage API
 * exists; only the configured limit is real. (Demo keeps the richer $used/$limit
 * illustration.)
 */
export async function getLlmBudgets(): Promise<LlmBudget[]> {
  return withDemo(MOCK_BUDGETS, async () => {
    const d = await postGraphQL<{ hub: { db: { llm_budgets: RawBudget[] } } }>(
      `query { hub { db { llm_budgets { id scope provider_id period max_tokens_in max_tokens_out max_requests } } } }`,
    )
    return d.hub.db.llm_budgets.map((b) => {
      const tokenLimit = (b.max_tokens_in ?? 0) + (b.max_tokens_out ?? 0)
      const limit = tokenLimit
        ? `${fmtTokens(tokenLimit)} tok/${b.period}`
        : b.max_requests
          ? `${b.max_requests} req/${b.period}`
          : `—/${b.period}`
      return {
        name: b.provider_id ? `${b.scope} · ${b.provider_id}` : b.scope,
        used: '—',
        limit,
        pct: 0,
      }
    })
  })
}

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 ? 1 : 0)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(n % 1_000 ? 1 : 0)}K`
  return String(n)
}

/* ── Recent activity feed ───────────────────────────────────── */

export type ActivityTag = 'AGENT' | 'HITL' | 'ARTIFACT' | 'PLATFORM' | 'FLEET'

export interface ActivityEvent {
  time: string
  tag: ActivityTag
  text: string
  /** CSS-var color token for the tag. */
  tagColor: string
}

const TAG_COLOR: Record<ActivityTag, string> = {
  AGENT: 'var(--accent)',
  HITL: 'var(--amber)',
  ARTIFACT: 'var(--blue)',
  PLATFORM: 'var(--red)',
  FLEET: 'var(--text2)',
}

function ev(time: string, tag: ActivityTag, text: string): ActivityEvent {
  return { time, tag, text, tagColor: TAG_COLOR[tag] }
}

const MOCK_ACTIVITY: ActivityEvent[] = [
  ev('09:41:12', 'AGENT', 'analytics-copilot completed turn in chat “Gateway outage triage” (↑ 3,882 ↓ 2,405)'),
  ev('09:38:02', 'HITL', 'm.keller approved http_post for analytics-copilot (INC-88231 webhook)'),
  ev('09:12:55', 'ARTIFACT', 'gateway_offline_24h.csv produced in chat c1'),
  ev('08:57:20', 'PLATFORM', 'data source “billing” failed health probe: connection refused (my-bill:3306)'),
  ev('08:41:07', 'FLEET', 'finance-qa desired=active → converge started (container scheduling)'),
]

export async function getRecentActivity(): Promise<ActivityEvent[]> {
  // No dedicated activity table in the contract yet — the real feed will read a
  // session/audit-event stream. Returns the seed in both modes for now.
  return withDemo(MOCK_ACTIVITY, async () => MOCK_ACTIVITY)
}
