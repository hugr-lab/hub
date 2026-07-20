import { forwardRef } from 'react'
import { Search } from 'lucide-react'
import { cn } from '@/lib/cn'

const base =
  'w-full rounded-btn border border-border2 bg-surface px-2.5 py-1.5 text-sm text-text placeholder:text-text3 ' +
  'focus:outline focus:outline-2 focus:outline-accent disabled:opacity-50'

export const Input = forwardRef<
  HTMLInputElement,
  React.InputHTMLAttributes<HTMLInputElement> & { mono?: boolean }
>(({ className, mono, ...props }, ref) => (
  <input ref={ref} className={cn(base, mono && 'font-mono', className)} {...props} />
))
Input.displayName = 'Input'

export const Textarea = forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement> & { mono?: boolean }
>(({ className, mono, ...props }, ref) => (
  <textarea
    ref={ref}
    className={cn(base, 'resize-y leading-relaxed', mono && 'font-mono', className)}
    {...props}
  />
))
Textarea.displayName = 'Textarea'

export const Select = forwardRef<
  HTMLSelectElement,
  React.SelectHTMLAttributes<HTMLSelectElement>
>(({ className, children, ...props }, ref) => (
  <select ref={ref} className={cn(base, 'cursor-pointer pr-7', className)} {...props}>
    {children}
  </select>
))
Select.displayName = 'Select'

export function Label({ className, ...props }: React.LabelHTMLAttributes<HTMLLabelElement>) {
  return (
    <label
      className={cn('mb-1 block text-xs font-medium text-text2', className)}
      {...props}
    />
  )
}

/** Field wrapper: label + optional hint + control. */
export function Field({
  label,
  hint,
  htmlFor,
  children,
  className,
}: {
  label?: string
  hint?: string
  htmlFor?: string
  children: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex flex-col', className)}>
      {label && <Label htmlFor={htmlFor}>{label}</Label>}
      {children}
      {hint && <p className="mt-1 text-2xs text-text3">{hint}</p>}
    </div>
  )
}

/** Search field: icon + borderless input inside a bordered pill. */
export function SearchField({
  className,
  ...props
}: React.InputHTMLAttributes<HTMLInputElement>) {
  return (
    <div
      className={cn(
        'flex items-center gap-2 rounded-btn border border-border2 bg-surface px-2.5',
        'focus-within:outline focus-within:outline-2 focus-within:outline-accent',
        className,
      )}
    >
      <Search className="h-3.5 w-3.5 flex-none text-text3" />
      <input
        className="w-full bg-transparent py-1.5 text-sm text-text placeholder:text-text3 focus:outline-none"
        {...props}
      />
    </div>
  )
}
