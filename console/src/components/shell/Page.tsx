import { cn } from '@/lib/cn'

/** Standard scrollable page: padding 20px 22px, column, gap. */
export function Page({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('flex flex-1 flex-col gap-4 overflow-y-auto px-[22px] py-5', className)}
      {...props}
    />
  )
}

/** Page header: title + subtitle on the left, actions on the right. */
export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title?: React.ReactNode
  subtitle?: React.ReactNode
  actions?: React.ReactNode
}) {
  return (
    <div className="flex items-start gap-3">
      <div className="min-w-0 flex-1">
        {title && <h1 className="text-base font-semibold tracking-[-0.01em]">{title}</h1>}
        {subtitle && <p className="mt-0.5 text-sm text-text2">{subtitle}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}

/** Subtle mono caption documenting the backing API call. */
export function ApiHint({ children }: { children: React.ReactNode }) {
  return <p className="pt-1 font-mono text-2xs text-text3">{children}</p>
}
