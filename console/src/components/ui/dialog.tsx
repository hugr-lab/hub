import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { cn } from '@/lib/cn'

/**
 * Centered modal — overlay + blur, r14, shadow-lg, fadeUp.
 */
export function Modal({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  width = 440,
  className,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  title?: React.ReactNode
  description?: React.ReactNode
  children?: React.ReactNode
  footer?: React.ReactNode
  width?: number
  className?: string
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-[rgba(10,16,15,0.45)] backdrop-blur-[2px]" />
        <Dialog.Content
          onOpenAutoFocus={(e) => e.preventDefault()}
          className={cn(
            'fixed left-1/2 top-1/2 z-50 flex max-h-[88vh] w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col',
            'rounded-modal border border-border bg-surface shadow-lg animate-fadeUp',
            className,
          )}
          style={{ maxWidth: width }}
        >
          {(title || description) && (
            <div className="flex items-start gap-3 border-b border-border px-4 py-3">
              <div className="flex-1">
                {title && (
                  <Dialog.Title className="text-sm font-semibold tracking-[-0.01em]">
                    {title}
                  </Dialog.Title>
                )}
                {description && (
                  <Dialog.Description className="mt-0.5 text-xs text-text2">
                    {description}
                  </Dialog.Description>
                )}
              </div>
              <Dialog.Close className="rounded-[6px] p-1 text-text3 hover:bg-surface2 hover:text-text">
                <X className="h-4 w-4" />
              </Dialog.Close>
            </div>
          )}
          <div className="min-h-0 flex-1 overflow-y-auto p-4">{children}</div>
          {footer && (
            <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-3">
              {footer}
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

/**
 * Right-slide drawer — overlay, left border, shadow-lg, backdrop close.
 */
export function Drawer({
  open,
  onOpenChange,
  title,
  subtitle,
  children,
  footer,
  width = 460,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  title?: React.ReactNode
  subtitle?: React.ReactNode
  children?: React.ReactNode
  footer?: React.ReactNode
  width?: number
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-[rgba(10,16,15,0.32)]" />
        <Dialog.Content
          onOpenAutoFocus={(e) => e.preventDefault()}
          className="fixed right-0 top-0 z-50 flex h-full w-[calc(100vw-2rem)] flex-col border-l border-border bg-surface shadow-lg animate-fadeUp"
          style={{ maxWidth: width }}
        >
          <div className="flex items-start gap-3 border-b border-border px-4 py-3">
            <div className="min-w-0 flex-1">
              {title && (
                <Dialog.Title className="truncate text-sm font-semibold tracking-[-0.01em]">
                  {title}
                </Dialog.Title>
              )}
              {subtitle && (
                <Dialog.Description className="mt-0.5 truncate font-mono text-xs text-text2">
                  {subtitle}
                </Dialog.Description>
              )}
            </div>
            <Dialog.Close className="rounded-[6px] p-1 text-text3 hover:bg-surface2 hover:text-text">
              <X className="h-4 w-4" />
            </Dialog.Close>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto p-4">{children}</div>
          {footer && (
            <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-3">
              {footer}
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
