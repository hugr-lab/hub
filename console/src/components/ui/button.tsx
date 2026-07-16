import { forwardRef } from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { cn } from '@/lib/cn'

const button = cva(
  'inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-btn font-medium transition-colors ' +
    'focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent disabled:pointer-events-none disabled:opacity-50',
  {
    variants: {
      variant: {
        primary: 'bg-accent text-accent-text font-semibold hover:bg-accent-hi',
        secondary: 'border border-border bg-surface text-text hover:bg-surface2',
        ghost: 'text-text2 hover:bg-surface2 hover:text-text',
        danger: 'bg-red text-white font-semibold hover:opacity-90',
        'danger-ghost': 'border border-border bg-surface text-red hover:bg-red-soft',
        green: 'bg-green-soft text-green font-semibold hover:opacity-90',
        amber: 'bg-amber-soft text-amber font-semibold hover:opacity-90',
      },
      size: {
        sm: 'h-7 px-2.5 text-xs',
        md: 'h-8 px-3 text-sm',
        lg: 'h-9 px-4 text-base',
        icon: 'h-[30px] w-[30px] rounded-[7px] p-0',
      },
    },
    defaultVariants: { variant: 'secondary', size: 'md' },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof button> {
  asChild?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return <Comp ref={ref} className={cn(button({ variant, size }), className)} {...props} />
  },
)
Button.displayName = 'Button'
