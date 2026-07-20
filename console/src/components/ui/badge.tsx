import { cn } from '@/lib/cn'

export type Tone = 'neutral' | 'green' | 'amber' | 'red' | 'blue' | 'accent'

const toneBg: Record<Tone, string> = {
  neutral: 'bg-surface3 text-text2',
  green: 'bg-green-soft text-green',
  amber: 'bg-amber-soft text-amber',
  red: 'bg-red-soft text-red',
  blue: 'text-blue',
  accent: 'bg-accent-soft text-accent',
}

/** status/verdict badge — soft bg + matching text, mono/uppercase. */
export function Badge({
  tone = 'neutral',
  mono = true,
  className,
  ...props
}: { tone?: Tone; mono?: boolean } & React.HTMLAttributes<HTMLSpanElement>) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-chip px-1.5 py-0.5 text-2xs font-semibold uppercase tracking-[0.03em]',
        mono && 'font-mono',
        toneBg[tone],
        tone === 'blue' && 'bg-[color-mix(in_srgb,var(--blue)_15%,transparent)]',
        className,
      )}
      {...props}
    />
  )
}

export type DotState = 'ready' | 'running' | 'loading' | 'starting' | 'error' | 'idle' | string

export function dotColor(state: DotState): string {
  switch (state) {
    case 'ready':
    case 'running':
    case 'active':
    case 'connected':
      return 'var(--green)'
    case 'loading':
    case 'starting':
    case 'waiting':
      return 'var(--amber)'
    case 'error':
    case 'failed':
      return 'var(--red)'
    default:
      return 'var(--text3)'
  }
}

/** Status dot (7–9px), pulses while loading/starting. */
export function Dot({
  state = 'idle',
  size = 7,
  className,
}: {
  state?: DotState
  size?: number
  className?: string
}) {
  const pulsing = state === 'loading' || state === 'starting' || state === 'waiting'
  return (
    <span
      className={cn('inline-block flex-none rounded-full', pulsing && 'animate-pulse', className)}
      style={{ width: size, height: size, background: dotColor(state) }}
    />
  )
}

/** Count pill (surface3 / text2). */
export function Pill({ className, ...props }: React.HTMLAttributes<HTMLSpanElement>) {
  return (
    <span
      className={cn(
        'inline-flex min-w-[18px] items-center justify-center rounded-full bg-surface3 px-1.5 text-2xs font-semibold text-text2',
        className,
      )}
      {...props}
    />
  )
}
