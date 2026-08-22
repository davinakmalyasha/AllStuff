import { forwardRef, type InputHTMLAttributes, type ReactNode } from 'react'
import { AlertCircle } from 'lucide-react'

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string
  error?: string
  hint?: string
  suffix?: ReactNode
}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ label, error, hint, suffix, className = '', id, ...rest }, ref) => {
    const inputId = id ?? (label ? `field-${label.replace(/\s+/g, '-').toLowerCase()}` : undefined)
    return (
      <div className="w-full">
        {label && (
          <label htmlFor={inputId} className="mb-1.5 block text-sm font-medium text-ink">
            {label}
          </label>
        )}
        <div className="relative">
          <input
            ref={ref}
            id={inputId}
            className={[
              'h-10 w-full rounded-lg border bg-surface px-3 text-sm text-ink',
              'placeholder:text-ink3 transition-colors',
              error
                ? 'border-red-400 focus:border-red-500'
                : 'border-border focus:border-ink',
              suffix ? 'pr-9' : '',
              className,
            ].join(' ')}
            aria-invalid={!!error}
            {...rest}
          />
          {suffix && (
            <div className="absolute inset-y-0 right-3 flex items-center">{suffix}</div>
          )}
        </div>
        {error && (
          <p className="mt-1.5 flex items-center gap-1 text-xs text-red-600 dark:text-red-400">
            <AlertCircle className="h-3 w-3" aria-hidden /> {error}
          </p>
        )}
        {hint && !error && <p className="mt-1.5 text-xs text-ink3">{hint}</p>}
      </div>
    )
  },
)
Input.displayName = 'Input'
