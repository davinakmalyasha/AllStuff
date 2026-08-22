import { type ReactNode } from 'react'

type Tone = 'neutral' | 'positive' | 'attention' | 'danger'

const toneClasses: Record<Tone, string> = {
  neutral: 'bg-surface2 text-ink2 border-border',
  positive: 'bg-transparent text-ink border-border',
  attention: 'border-ink bg-surface text-ink',
  danger: 'text-red-600 dark:text-red-400 border-red-300 bg-red-50 dark:bg-red-950/30',
}

export function Badge({
  children,
  tone = 'neutral',
  dot,
  className = '',
}: {
  children: ReactNode
  tone?: Tone
  dot?: boolean
  className?: string
}) {
  return (
    <span
      className={[
        'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5',
        'text-xs font-medium',
        toneClasses[tone],
        className,
      ].join(' ')}
    >
      {dot && <span className="h-1.5 w-1.5 rounded-full bg-current animate-pulseDot" aria-hidden />}
      {children}
    </span>
  )
}
