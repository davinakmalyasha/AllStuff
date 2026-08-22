import { type ButtonHTMLAttributes, forwardRef } from 'react'

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger'
type Size = 'sm' | 'md' | 'lg'

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
  fullWidth?: boolean
}

const variantClasses: Record<Variant, string> = {
  primary:
    'bg-accent text-accent-ink hover:opacity-85 active:opacity-70 disabled:opacity-40',
  secondary:
    'bg-transparent border border-border text-ink hover:bg-surface2 active:bg-surface2 disabled:opacity-40',
  ghost: 'bg-transparent text-ink hover:bg-surface2 disabled:opacity-40',
  danger:
    'bg-transparent border border-border text-red-700 dark:text-red-400 hover:border-red-300 hover:bg-red-50 dark:hover:bg-red-950/30 disabled:opacity-40',
}

const sizeClasses: Record<Size, string> = {
  sm: 'h-8 px-3 text-sm',
  md: 'h-10 px-4 text-sm',
  lg: 'h-12 px-6 text-base',
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ variant = 'primary', size = 'md', fullWidth, className = '', type = 'button', ...rest }, ref) => (
    <button
      ref={ref}
      type={type}
      className={[
        'inline-flex items-center justify-center gap-2 rounded-lg font-medium',
        'transition-colors duration-150 select-none',
        'disabled:cursor-not-allowed',
        variantClasses[variant],
        sizeClasses[size],
        fullWidth ? 'w-full' : '',
        className,
      ].join(' ')}
      {...rest}
    />
  ),
)
Button.displayName = 'Button'
