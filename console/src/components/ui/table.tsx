import { cn } from '@/lib/cn'
import { EmptyState } from './feedback'

export interface Column<Row> {
  key: string
  header: React.ReactNode
  /** CSS grid track for this column, e.g. '1fr', '120px', 'minmax(0,1.4fr)'. */
  width?: string
  align?: 'left' | 'right' | 'center'
  cell: (row: Row, index: number) => React.ReactNode
}

/**
 * CSS-grid data table: eyebrow header row + grid rows with hairline separators.
 * Matches the prototype's dense operator tables.
 */
export function DataTable<Row>({
  columns,
  rows,
  getKey,
  onRowClick,
  empty,
  className,
}: {
  columns: Column<Row>[]
  rows: Row[]
  getKey: (row: Row, index: number) => string
  onRowClick?: (row: Row) => void
  empty?: React.ReactNode
  className?: string
}) {
  const template = columns.map((c) => c.width ?? '1fr').join(' ')
  const alignCls = (a?: Column<Row>['align']) =>
    a === 'right' ? 'justify-end text-right' : a === 'center' ? 'justify-center text-center' : ''

  return (
    <div className={cn('overflow-hidden rounded-card border border-border bg-surface', className)}>
      <div className="min-w-0 overflow-x-auto">
        {/* header */}
        <div
          className="grid items-center gap-3 border-b border-border bg-surface2 px-4 py-2"
          style={{ gridTemplateColumns: template }}
        >
          {columns.map((c) => (
            <div key={c.key} className={cn('eyebrow flex items-center', alignCls(c.align))}>
              {c.header}
            </div>
          ))}
        </div>
        {/* rows */}
        {rows.length === 0 ? (
          <div className="p-8">{empty ?? <EmptyState title="Nothing here yet" />}</div>
        ) : (
          rows.map((row, i) => (
            <div
              key={getKey(row, i)}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
              className={cn(
                'grid items-center gap-3 border-b border-border px-4 py-2.5 text-sm last:border-b-0',
                onRowClick && 'cursor-pointer hover:bg-surface2',
              )}
              style={{ gridTemplateColumns: template }}
            >
              {columns.map((c) => (
                <div key={c.key} className={cn('flex min-w-0 items-center', alignCls(c.align))}>
                  {c.cell(row, i)}
                </div>
              ))}
            </div>
          ))
        )}
      </div>
    </div>
  )
}
