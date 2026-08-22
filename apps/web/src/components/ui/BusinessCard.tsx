import { Link } from 'react-router-dom'
import { ShieldCheck, Star } from 'lucide-react'
import type { BusinessDTO } from '@/lib/api'
import { Badge } from './Badge'
import { useCompare } from '@/stores/compare'

/** Business card used across search results, category pages, and similar lists. */
export function BusinessCard({ business: b, compare = false }: { business: BusinessDTO; compare?: boolean }) {
  const { ids, toggle } = useCompare()
  const inCompare = ids.includes(b.id)
  return (
    <div className="card group relative p-4 transition-shadow duration-200 hover:shadow-cardHover">
      {compare && (
        <button
          onClick={(e) => {
            e.preventDefault()
            toggle(b.id)
          }}
          className={`absolute right-3 top-3 z-10 flex h-7 w-7 items-center justify-center rounded-md border text-xs font-mono transition-colors ${
            inCompare ? 'border-ink bg-accent text-accent-ink' : 'border-border bg-surface text-ink3 hover:text-ink'
          }`}
          aria-label={inCompare ? 'Remove from compare' : 'Add to compare'}
          title="Compare"
        >
          {inCompare ? '✓' : '+'}
        </button>
      )}
      <Link to={`/b/${b.slug}`} className="flex gap-4">
        {b.logo_url ? (
          <img src={b.logo_url} alt="" className="h-14 w-14 shrink-0 rounded-xl object-cover" loading="lazy" />
        ) : (
          <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-xl bg-surface2 text-lg font-semibold text-ink2">
            {b.name.charAt(0)}
          </div>
        )}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <p className="truncate text-sm font-semibold text-ink group-hover:underline">{b.name}</p>
            {b.verification_level && (
              <ShieldCheck className="h-3.5 w-3.5 shrink-0 text-ink3" aria-label={b.verification_level === 'fully_verified' ? 'Fully verified' : 'Verified'} />
            )}
          </div>
          <p className="mt-0.5 truncate text-xs text-ink3">
            {b.category_name} · {b.city}
          </p>
          <div className="mt-1.5 flex flex-wrap items-center gap-2 text-xs">
            {b.review_count > 0 && (
              <span className="flex items-center gap-1 text-ink2">
                <Star className="h-3 w-3" /> {b.rating_avg?.toFixed(1)}
              </span>
            )}
            <span className="font-mono text-ink3">{"$".repeat(b.price_level ?? 0) || '—'}</span>
            {b.distance_km !== null && b.distance_km !== undefined && (
              <span className="text-ink3">{b.distance_km.toFixed(1)} km</span>
            )}
            <Badge tone={b.is_open_now ? 'positive' : 'neutral'} dot>{b.is_open_now ? 'Open' : 'Closed'}</Badge>
          </div>
        </div>
      </Link>
    </div>
  )
}
