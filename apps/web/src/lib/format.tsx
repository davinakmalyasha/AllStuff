/** Relative time ("2h ago") — Batch 3 polish. */
export function timeAgo(iso: string): string {
  const then = new Date(iso).getTime()
  const diff = Date.now() - then
  const mins = Math.floor(diff / 60_000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d ago`
  return new Date(iso).toLocaleDateString()
}

/** Card skeleton (Batch 3). */
export function SkeletonCard() {
  return (
    <div className="card p-4">
      <div className="h-14 w-14 animate-pulse rounded-xl bg-surface2" />
      <div className="mt-3 h-4 w-3/4 animate-pulse rounded bg-surface2" />
      <div className="mt-2 h-3 w-1/2 animate-pulse rounded bg-surface2" />
    </div>
  )
}

/** Copy to clipboard with feedback (Batch 1 share fix). */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    // Fallback for non-secure contexts.
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
      return true
    } catch {
      return false
    }
  }
}
