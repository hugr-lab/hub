import { postGraphQL } from '@/lib/graphql'
import { withDemo } from '@/lib/demo'

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
    // `disabled` column if that call is unavailable.
    const statuses = await fetchDataSourceStatuses(rows.map((r) => r.name)).catch(
      () => ({}) as Record<string, string>,
    )
    return rows.map((r) => ({
      name: r.name,
      type: r.type,
      path: r.path,
      status: statuses[r.name] ?? (r.disabled ? 'disabled' : 'unknown'),
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
  name: string
  status: string
}

export async function getFleetRollup(): Promise<FleetRow[]> {
  return withDemo(MOCK_FLEET, async () => {
    const d = await postGraphQL<{ hub: { my_agent_instances: RawAgentInstance[] } }>(
      `query { hub { my_agent_instances { id name status } } }`,
    )
    return d.hub.my_agent_instances.map((a) => ({
      id: a.id,
      name: a.name,
      // TODO: session count is not part of the documented `my_agent_instances`
      // shape — surface it once the fleet table exposes an active-session field.
      sessions: 0,
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
  name: string
  used: number
  limit: number
}

export async function getLlmBudgets(): Promise<LlmBudget[]> {
  return withDemo(MOCK_BUDGETS, async () => {
    const d = await postGraphQL<{ hub: { db: { llm_budgets: RawBudget[] } } }>(
      `query { hub { db { llm_budgets { name used limit } } } }`,
    )
    return d.hub.db.llm_budgets.map((b) => ({
      name: b.name,
      used: fmtUsd(b.used),
      limit: fmtUsd(b.limit),
      pct: b.limit > 0 ? Math.round((b.used / b.limit) * 100) : 0,
    }))
  })
}

function fmtUsd(n: number): string {
  return `$${Math.round(n)}`
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
