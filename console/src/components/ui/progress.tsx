import { cn } from '@/lib/cn'

/** Progress bar — 5px track, colored fill. */
export function Progress({
  value,
  color = 'var(--accent)',
  className,
}: {
  value: number // 0..100
  color?: string
  className?: string
}) {
  return (
    <div className={cn('h-[5px] w-full overflow-hidden rounded-full bg-surface3', className)}>
      <div
        className="h-full rounded-full transition-[width]"
        style={{ width: `${Math.max(0, Math.min(100, value))}%`, background: color }}
      />
    </div>
  )
}
