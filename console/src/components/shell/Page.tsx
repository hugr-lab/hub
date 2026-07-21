import { cn } from '@/lib/cn'

/** Standard scrollable page: padding 20px 22px, column, gap. */
export function Page({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    // The scroll container must be a plain block, NOT a flex column: a flex
    // column would let tall children (a DataTable's `overflow-hidden` wrapper)
    // flex-shrink to fit and clip their rows, so there'd be nothing to scroll.
    // As a block, the inner content keeps its full height; min-h-0 + flex-1 bound
    // this element to the shell's `main`, and overflow-y-auto then scrolls it.
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className={cn('flex flex-col gap-4 px-[22px] py-5', className)} {...props} />
    </div>
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
