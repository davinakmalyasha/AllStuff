export function Spinner({ className = '' }: { className?: string }) {
  return (
    <svg
      className={`h-4 w-4 animate-spin ${className}`}
      viewBox="0 0 24 24"
      fill="none"
      aria-label="Loading"
      role="status"
    >
      <circle className="opacity-20" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" />
      <path className="opacity-90" d="M22 12a10 10 0 0 0-10-10" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  )
}

export function PageSpinner({ label }: { label?: string }) {
  return (
    <div className="flex min-h-[40vh] flex-col items-center justify-center gap-3 text-ink2">
      <Spinner className="h-6 w-6" />
      {label && <p className="text-sm">{label}</p>}
    </div>
  )
}

/** Inline error note with retry — the standard failure UI for query surfaces. */
export function ErrorNote({
  message = 'Something went wrong loading this.',
  onRetry,
}: {
  message?: string
  onRetry?: () => void
}) {
  return (
    <div role="alert" className="flex min-h-[24vh] flex-col items-center justify-center gap-3 py-10 text-center">
      <p className="text-sm text-ink2">{message}</p>
      {onRetry && (
        <button
          onClick={onRetry}
          className="rounded-lg border border-border px-3 py-1.5 text-sm text-ink hover:bg-surface2"
        >
          Try again
        </button>
      )}
    </div>
  )
}

export { SkeletonCard } from '@/lib/format'
