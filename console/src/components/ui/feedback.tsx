import { Loader2 } from 'lucide-react'
import { cn } from '@/lib/cn'

/** Empty state — dashed box, muted. */
export function EmptyState({
  title,
  description,
  action,
  icon,
  className,
}: {
  title: string
  description?: string
  action?: React.ReactNode
  icon?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-2 rounded-card border border-dashed border-border2 px-6 py-10 text-center',
        className,
      )}
    >
      {icon && <div className="text-text3">{icon}</div>}
      <div className="text-sm font-medium text-text2">{title}</div>
      {description && <div className="max-w-sm text-xs text-text3">{description}</div>}
      {action && <div className="mt-1">{action}</div>}
    </div>
  )
}

export type BannerTone = 'info' | 'error' | 'reveal'

const bannerCls: Record<BannerTone, string> = {
  info: 'border-accent/40 bg-accent-soft text-text',
  error: 'border-red/40 bg-red-soft text-text',
  reveal: 'border-green/40 bg-green-soft text-text',
}

/** Info / error / reveal alert banner (soft bg + border). */
export function Banner({
  tone = 'info',
  children,
  className,
  ...props
}: { tone?: BannerTone } & React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('rounded-btn border px-3 py-2 text-sm', bannerCls[tone], className)}
      {...props}
    >
      {children}
    </div>
  )
}

export function Spinner({ className, size = 14 }: { className?: string; size?: number }) {
  return <Loader2 className={cn('animate-spin text-text3', className)} style={{ width: size, height: size }} />
}
