import { cn } from '@/lib/cn'

export function Card({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('rounded-card border border-border bg-surface shadow-card', className)}
      {...props}
    />
  )
}

export function CardHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('flex items-center gap-3 border-b border-border px-4 py-3', className)}
      {...props}
    />
  )
}

export function CardTitle({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('text-sm font-semibold tracking-[-0.01em]', className)} {...props} />
}

export function CardBody({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('p-4', className)} {...props} />
}

/** Uppercase 10.5px eyebrow label. */
export function Eyebrow({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('eyebrow', className)} {...props} />
}

/** Dashboard stat tile: eyebrow + big number + colored sub-note. */
export function StatTile({
  label,
  value,
  sub,
  subColor,
}: {
  label: string
  value: string
  sub?: string
  subColor?: string
}) {
  return (
    <Card className="p-4">
      <div className="eyebrow">{label}</div>
      <div className="mt-1.5 font-mono text-2xl font-semibold tracking-[-0.02em]">{value}</div>
      {sub && (
        <div className="mt-1 text-xs" style={{ color: subColor ?? 'var(--text3)' }}>
          {sub}
        </div>
      )}
    </Card>
  )
}
