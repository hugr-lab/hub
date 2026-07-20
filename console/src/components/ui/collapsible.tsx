import { useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { cn } from '@/lib/cn'

/** Collapsible disclosure with a rotating chevron. */
export function Collapsible({
  header,
  defaultOpen = false,
  open: controlledOpen,
  onOpenChange,
  children,
  className,
  headerClassName,
}: {
  header: React.ReactNode
  defaultOpen?: boolean
  open?: boolean
  onOpenChange?: (o: boolean) => void
  children: React.ReactNode
  className?: string
  headerClassName?: string
}) {
  const [uncontrolled, setUncontrolled] = useState(defaultOpen)
  const open = controlledOpen ?? uncontrolled
  const setOpen = (o: boolean) => {
    onOpenChange?.(o)
    if (controlledOpen === undefined) setUncontrolled(o)
  }

  return (
    <div className={className}>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className={cn(
          'flex w-full items-center gap-1.5 text-left',
          headerClassName,
        )}
      >
        <ChevronRight
          className={cn('h-3.5 w-3.5 flex-none text-text3 transition-transform', open && 'rotate-90')}
        />
        <span className="min-w-0 flex-1">{header}</span>
      </button>
      {open && <div>{children}</div>}
    </div>
  )
}
