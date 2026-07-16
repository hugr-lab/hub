import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Page, ApiHint } from '@/components/shell/Page'
import {
  Card,
  CardHeader,
  CardTitle,
  StatTile,
  Dot,
  dotColor,
  Progress,
  Spinner,
  EmptyState,
} from '@/components/ui'
import {
  getMonitoringStats,
  getDataSourceHealth,
  getFleetRollup,
  getLlmBudgets,
  getRecentActivity,
  type StatTileData,
} from '@/api/monitoring'

const STAT_SKELETON: StatTileData[] = [
  'Agents running',
  'Active sessions',
  'Tokens · 24h',
  'Data sources ready',
].map((label) => ({ label, value: '—', sub: '', subColor: 'var(--text3)' }))

function budgetColor(pct: number): string {
  if (pct >= 100) return 'var(--red)'
  if (pct >= 90) return 'var(--amber)'
  return 'var(--accent)'
}

/** Card with a header row and body that handles loading / empty states. */
function ListCard({
  title,
  right,
  loading,
  isEmpty,
  emptyLabel,
  className,
  children,
}: {
  title: string
  right?: ReactNode
  loading: boolean
  isEmpty: boolean
  emptyLabel: string
  className?: string
  children: ReactNode
}) {
  return (
    <Card className={`overflow-hidden ${className ?? ''}`}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {right && <div className="ml-auto">{right}</div>}
      </CardHeader>
      {loading ? (
        <div className="flex items-center justify-center py-8">
          <Spinner />
        </div>
      ) : isEmpty ? (
        <div className="p-4">
          <EmptyState title={emptyLabel} />
        </div>
      ) : (
        <div>{children}</div>
      )}
    </Card>
  )
}

export function MonitoringScreen() {
  const stats = useQuery({ queryKey: ['monitoring', 'stats'], queryFn: getMonitoringStats })
  const dsHealth = useQuery({ queryKey: ['monitoring', 'dsHealth'], queryFn: getDataSourceHealth })
  const fleet = useQuery({ queryKey: ['monitoring', 'fleet'], queryFn: getFleetRollup })
  const budgets = useQuery({ queryKey: ['monitoring', 'budgets'], queryFn: getLlmBudgets })
  const activity = useQuery({ queryKey: ['monitoring', 'activity'], queryFn: getRecentActivity })

  const tiles = stats.data ?? STAT_SKELETON

  return (
    <Page>
      {/* Stat tiles */}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {tiles.map((t) => (
          <StatTile key={t.label} label={t.label} value={t.value} sub={t.sub} subColor={t.subColor} />
        ))}
      </div>

      {/* Data source health + Fleet / budgets */}
      <div className="grid items-start gap-3.5 lg:grid-cols-[1.4fr_1fr]">
        <ListCard
          title="Data source health"
          right={
            <Link
              to="/platform/data-sources"
              className="text-xs font-medium text-accent hover:text-accent-hi"
            >
              Open data sources →
            </Link>
          }
          loading={dsHealth.isLoading}
          isEmpty={!dsHealth.data?.length}
          emptyLabel="No data sources"
        >
          {dsHealth.data?.map((d) => (
            <div
              key={d.name}
              className="grid grid-cols-[14px_minmax(0,1.3fr)_minmax(0,0.8fr)_minmax(0,1fr)_auto] items-center gap-2.5 border-b border-border px-4 py-2 text-sm last:border-b-0"
            >
              <Dot state={d.status} size={8} />
              <span className="min-w-0 truncate font-mono text-xs font-semibold">{d.name}</span>
              <span className="min-w-0 truncate text-text2">{d.type}</span>
              <span className="min-w-0 truncate text-xs text-text3">{d.path}</span>
              <span
                className="text-2xs font-semibold uppercase tracking-[0.04em]"
                style={{ color: dotColor(d.status) }}
              >
                {d.status}
              </span>
            </div>
          ))}
        </ListCard>

        <div className="flex flex-col gap-3.5">
          <ListCard
            title="Fleet"
            loading={fleet.isLoading}
            isEmpty={!fleet.data?.length}
            emptyLabel="No accessible agents"
          >
            {fleet.data?.map((a) => (
              <div
                key={a.id}
                className="flex items-center gap-2.5 border-b border-border px-4 py-2 text-sm last:border-b-0"
              >
                <Dot state={a.runtime} size={8} />
                <span className="min-w-0 flex-1 truncate font-semibold">{a.name}</span>
                {a.sessions > 0 && <span className="text-xs text-text3">{a.sessions} sess</span>}
                <span className="text-2xs font-semibold" style={{ color: dotColor(a.runtime) }}>
                  {a.runtime}
                </span>
              </div>
            ))}
          </ListCard>

          <ListCard
            title="LLM budgets"
            right={<span className="font-mono text-2xs text-text3">hub.db.llm_budgets</span>}
            loading={budgets.isLoading}
            isEmpty={!budgets.data?.length}
            emptyLabel="No budgets configured"
          >
            {budgets.data?.map((b) => (
              <div
                key={b.name}
                className="flex flex-col gap-1.5 border-b border-border px-4 py-2.5 last:border-b-0"
              >
                <div className="flex items-baseline text-sm">
                  <span className="font-semibold">{b.name}</span>
                  <span className="ml-auto font-mono text-xs text-text2">
                    {b.used} / {b.limit}
                  </span>
                </div>
                <Progress value={b.pct} color={budgetColor(b.pct)} />
              </div>
            ))}
          </ListCard>
        </div>
      </div>

      {/* Recent activity */}
      <ListCard
        title="Recent activity"
        loading={activity.isLoading}
        isEmpty={!activity.data?.length}
        emptyLabel="No recent activity"
      >
        {activity.data?.map((e, i) => (
          <div
            key={`${e.time}-${i}`}
            className="grid grid-cols-[100px_84px_minmax(0,1fr)] items-baseline gap-3 border-b border-border px-4 py-2 text-sm last:border-b-0"
          >
            <span className="font-mono text-xs text-text3">{e.time}</span>
            <span
              className="text-2xs font-bold uppercase tracking-[0.05em]"
              style={{ color: e.tagColor }}
            >
              {e.tag}
            </span>
            <span className="min-w-0 text-text2">{e.text}</span>
          </div>
        ))}
      </ListCard>

      <div>
        <ApiHint>hub.my_agent_instances · core.data_sources · data_source_status(name)</ApiHint>
        <ApiHint>hub.db.llm_budgets · recent activity = session / audit event feed</ApiHint>
      </div>
    </Page>
  )
}
