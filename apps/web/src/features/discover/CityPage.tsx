import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, type BusinessDTO, type TrendEntryDTO } from '@/lib/api'
import { ErrorNote, PageSpinner } from '@/components/ui/Spinner'
import { BusinessCard } from '@/components/ui/BusinessCard'
import { usePageMeta, useJsonLd } from '@/lib/meta'

interface CityCategory {
  category_id: string
  category_name: string
  category_slug: string
  count: number
}

interface CityPageDTO {
  slug: string
  name: string
  count: number
  lat: number | null
  lng: number | null
  categories: CityCategory[]
  top: TrendEntryDTO[]
  top_rated: BusinessDTO[]
  updated_at: string
}

/** City landing page (PRD §5.1.5): trending leaders, categories, map. */
export function CityPage() {
  const { slug = '' } = useParams()

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['city', slug],
    queryFn: () => api<{ city: CityPageDTO }>(`/cities/${slug}`),
  })

  const city = data?.city
  usePageMeta(city ? `Businesses in ${city.name} — BizVerse` : 'Cities', undefined, {
    url: city ? `${window.location.origin}/city/${city.slug}` : undefined,
  })
  useJsonLd(
    city
      ? {
          '@context': 'https://schema.org',
          '@type': 'Place',
          name: city.name,
          address: { '@type': 'PostalAddress', addressLocality: city.name },
          geo: city.lat && city.lng ? { '@type': 'GeoCoordinates', latitude: city.lat, longitude: city.lng } : undefined,
        }
      : null,
  )

  if (isLoading) return <PageSpinner />
  // Server errors rendered as "404" hid outages; distinguish and offer retry.
  if (isError) return <ErrorNote message="Something went wrong loading this city." onRetry={() => void refetch()} />
  if (!city) {
    return (
      <div className="container-page flex min-h-[40vh] flex-col items-center justify-center gap-2 text-center">
        <p className="font-mono text-5xl font-semibold tracking-tight">404</p>
        <p className="text-sm text-ink2">City not found.</p>
      </div>
    )
  }

  return (
    <div className="container-page py-10">
      <p className="mono-label mb-1">City</p>
      <h1 className="text-2xl font-semibold tracking-tight">{city.name}</h1>
      <p className="mt-1 text-sm text-ink2">{city.count} verified business{city.count === 1 ? '' : 'es'}</p>
      {city.updated_at && (
        <p className="mt-1 text-xs text-ink3">
          trending updated {Math.max(1, Math.round((Date.now() - new Date(city.updated_at).getTime()) / 60000))} min ago
        </p>
      )}

      <div className="mt-8 grid gap-8 lg:grid-cols-[1fr_280px]">
        <div className="space-y-10">
          {city.top.length > 0 && (
            <section>
              <h2 className="mono-label mb-3">Trending in {city.name}</h2>
              <div className="grid gap-4 sm:grid-cols-2">
                {city.top.map((e, i) => (
                  <div key={e.id} className="relative">
                    <span className="absolute -left-2 -top-2 z-10 flex h-7 w-7 items-center justify-center rounded-full bg-accent font-mono text-xs font-semibold text-accent-ink">
                      {i + 1}
                    </span>
                    <BusinessCard business={e as unknown as BusinessDTO} />
                    {e.is_rising && (
                      <span className="absolute -right-2 -top-2 z-10 rounded-full border border-border bg-surface px-2 py-0.5 text-[10px] font-semibold text-ink2">
                        ▲ Rising
                      </span>
                    )}
                  </div>
                ))}
              </div>
            </section>
          )}

          <section>
            <h2 className="mono-label mb-3">Top rated</h2>
            {city.top_rated.length === 0 ? (
              <p className="text-sm text-ink3">No businesses listed here yet.</p>
            ) : (
              <div className="grid gap-4 sm:grid-cols-2">
                {city.top_rated.map((b) => (
                  <BusinessCard key={b.id} business={b} />
                ))}
              </div>
            )}
          </section>
        </div>

        <aside className="space-y-6">
          <div>
            <h2 className="mono-label mb-3">Categories</h2>
            <ul className="space-y-1.5">
              {city.categories.map((c) => (
                <li key={c.category_id}>
                  <Link to={`/c/${c.category_slug}`} className="flex items-center justify-between text-sm text-ink2 hover:text-ink">
                    <span>{c.category_name}</span>
                    <span className="font-mono text-xs text-ink3">{c.count}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </div>
          {city.lat && city.lng && (
            <div className="overflow-hidden rounded-xl border border-border">
              {/* staticmap.openstreetmap.de is discontinued (see MiniMapLive);
                  hide silently when the tile never loads. */}
              <img
                src={`https://staticmap.openstreetmap.de/staticmap.php?center=${city.lat},${city.lng}&zoom=12&size=280x180`}
                alt={`Map of ${city.name}`}
                className="h-44 w-full object-cover"
                loading="lazy"
                onError={(e) => { (e.target as HTMLImageElement).style.display = 'none' }}
              />
            </div>
          )}
          <Link to={`/discover?city=${encodeURIComponent(city.name)}`} className="block text-sm text-ink underline underline-offset-4 hover:text-ink2">
            Browse all in {city.name} →
          </Link>
        </aside>
      </div>
    </div>
  )
}
