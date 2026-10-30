"use client"

export default function Error({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  return (
    <div className="container-page flex min-h-[50vh] flex-col items-center justify-center gap-3 text-center">
      <p className="font-mono text-5xl font-semibold tracking-tight">500</p>
      <p className="text-sm text-ink2">Something went wrong. Please try again.</p>
      {process.env.NODE_ENV === 'development' ? (
        <pre className="max-w-full overflow-auto text-left text-xs text-ink3">{error.message}</pre>
      ) : null}
      <button
        onClick={() => reset()}
        className="mt-2 inline-flex h-8 items-center rounded-lg border border-border bg-surface px-3 text-sm hover:bg-surface2"
      >
        Try again
      </button>
    </div>
  )
}
