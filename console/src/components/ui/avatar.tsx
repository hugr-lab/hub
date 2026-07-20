import { cn } from '@/lib/cn'

/** Accent circle + initials. */
export function Avatar({
  initials,
  size = 24,
  className,
}: {
  initials: string
  size?: number
  className?: string
}) {
  return (
    <span
      className={cn(
        'inline-flex flex-none items-center justify-center rounded-full bg-accent font-bold text-accent-text',
        className,
      )}
      style={{ width: size, height: size, fontSize: size * 0.42 }}
    >
      {initials}
    </span>
  )
}

/** Derive up-to-2-char initials from a display name. */
export function initialsOf(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) return '?'
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase()
}
