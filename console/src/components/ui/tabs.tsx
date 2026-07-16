import { cn } from '@/lib/cn'

export interface TabDef<T extends string> {
  value: T
  label: React.ReactNode
}

/** Underline tabs. */
export function Tabs<T extends string>({
  tabs,
  value,
  onChange,
  className,
}: {
  tabs: TabDef<T>[]
  value: T
  onChange: (v: T) => void
  className?: string
}) {
  return (
    <div className={cn('flex items-center gap-1 border-b border-border', className)} role="tablist">
      {tabs.map((t) => {
        const active = t.value === value
        return (
          <button
            key={t.value}
            role="tab"
            aria-selected={active}
            onClick={() => onChange(t.value)}
            className={cn(
              '-mb-px border-b-2 px-2.5 pb-2 pt-1.5 text-sm font-medium transition-colors',
              active
                ? 'border-accent text-text'
                : 'border-transparent text-text2 hover:text-text',
            )}
          >
            {t.label}
          </button>
        )
      })}
    </div>
  )
}
