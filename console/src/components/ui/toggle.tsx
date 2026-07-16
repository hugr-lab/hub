import * as Switch from '@radix-ui/react-switch'
import * as Checkbox from '@radix-ui/react-checkbox'
import { Check } from 'lucide-react'
import { cn } from '@/lib/cn'

export function Toggle({
  checked,
  onCheckedChange,
  disabled,
  className,
}: {
  checked: boolean
  onCheckedChange: (v: boolean) => void
  disabled?: boolean
  className?: string
}) {
  return (
    <Switch.Root
      checked={checked}
      onCheckedChange={onCheckedChange}
      disabled={disabled}
      className={cn(
        'relative h-[18px] w-[30px] flex-none rounded-full border border-border2 bg-surface2 transition-colors',
        'data-[state=checked]:border-accent data-[state=checked]:bg-accent disabled:opacity-50',
        className,
      )}
    >
      <Switch.Thumb className="block h-3 w-3 translate-x-[2px] rounded-full bg-surface shadow transition-transform data-[state=checked]:translate-x-[14px] data-[state=checked]:bg-accent-text" />
    </Switch.Root>
  )
}

export function CheckboxBox({
  checked,
  onCheckedChange,
  disabled,
  className,
}: {
  checked: boolean
  onCheckedChange: (v: boolean) => void
  disabled?: boolean
  className?: string
}) {
  return (
    <Checkbox.Root
      checked={checked}
      onCheckedChange={(v) => onCheckedChange(v === true)}
      disabled={disabled}
      className={cn(
        'flex h-4 w-4 flex-none items-center justify-center rounded-[4px] border border-border2 bg-surface transition-colors',
        'data-[state=checked]:border-accent data-[state=checked]:bg-accent disabled:opacity-50',
        className,
      )}
    >
      <Checkbox.Indicator>
        <Check className="h-3 w-3 text-accent-text" strokeWidth={2.5} />
      </Checkbox.Indicator>
    </Checkbox.Root>
  )
}
