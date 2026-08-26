import { Link } from 'react-router-dom'
import { ArrowRight } from 'lucide-react'
import { useCompare } from '@/stores/compare'

/** Floating tray (PRD §5.1.5): persistent across result pages. */
export function CompareTray() {
  const { ids, toggle, clear } = useCompare()
  if (ids.length === 0) return null
  return (
    <div className="fixed bottom-[calc(3.5rem+1rem)] left-1/2 z-50 flex -translate-x-1/2 items-center gap-3 rounded-xl border border-border bg-surface px-4 py-2.5 shadow-cardHover md:bottom-4">
      <span className="text-sm text-ink2">
        <span className="font-mono font-semibold text-ink">{ids.length}</span>/4 selected
      </span>
      <div className="flex gap-1">
        {ids.map((id) => (
          <button
            key={id}
            onClick={() => toggle(id)}
            className="flex h-6 w-6 items-center justify-center rounded-full bg-surface2 font-mono text-[10px] text-ink2 hover:bg-accent hover:text-accent-ink"
            aria-label={`Remove business ${id.slice(0, 8)}`}
          >
            {id.slice(0, 2)}
          </button>
        ))}
      </div>
      <Link to={`/compare?b=${ids.join(',')}`} className="rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-accent-ink">
        Compare <ArrowRight className="ml-0.5 inline h-3 w-3" />
      </Link>
      <button onClick={clear} className="text-xs text-ink3 hover:text-ink">Clear</button>
    </div>
  )
}
