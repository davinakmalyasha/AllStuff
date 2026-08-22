import { useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { GripVertical, Minus, Link2 } from 'lucide-react'
import { api, type BusinessDTO } from '@/lib/api'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner } from '@/components/ui/Spinner'
import { usePageMeta } from '@/lib/meta'
import { useCompare } from '@/stores/compare'

/** Side-by-side comparison (PRD §5.1.5, §6.1 /compare?b=…). URL is shareable. */
export function ComparePage() {
  const [params, setParams] = useSearchParams()
  const { ids, setIds } = useCompare()
  const [dragIdx, setDragIdx] = useState<number | null>(null)
  const overIdx = useRef<number | null>(null)

  const queryIds = (params.get('b') ?? '').split(',').filter(Boolean)
  const activeIds = queryIds.length >= 2 ? queryIds : ids

  usePageMeta('Compare businesses')

  const { data, isLoading } = useQuery({
    queryKey: ['compare', activeIds.join(',')],
    queryFn: () => api<{ businesses: BusinessDTO[] }>(`/compare?b=${activeIds.join(',')}`),
    enabled: activeIds.length >= 2,
  })

  const businesses = data?.businesses ?? []

  const updateUrl = () => {
    const next = new URLSearchParams(params)
    if (ids.length >= 2) next.set('b', ids.join(','))
    else next.delete('b')
    setParams(next, { replace: true })
  }

  const share = async () => {
    const url = `${window.location.origin}/compare?b=${activeIds.join(',')}`
    try {
      await navigator.clipboard.writeText(url)
    } catch {
      /* noop */
    }
  }

  // Drag a column header to reorder (updates the shareable URL).
  const drop = (to: number) => {
    setDragIdx(null)
    overIdx.current = null
    if (dragIdx === null || dragIdx === to) return
    const next = [...ids]
    const [moved] = next.splice(dragIdx, 1)
    next.splice(to, 0, moved)
    setIds(next)
    const url = new URLSearchParams(params)
    url.set('b', next.join(','))
    setParams(url, { replace: true })
  }
  const row = (label: string, cell: (b: BusinessDTO) => React.ReactNode) => (
    <div className="grid gap-3 border-b border-border py-3" style={{ gridTemplateColumns: `140px repeat(${businesses.length}, 1fr)` }}>
      <p className="text-xs font-medium uppercase tracking-wider text-ink3">{label}</p>
      {businesses.map((b) => (
        <div key={b.id} className="min-w-0 text-sm text-ink2">{cell(b)}</div>
      ))}
    </div>
  )

  if (isLoading) return <PageSpinner />
  if (businesses.length < 2) {
    return (
      <div className="container-page py-16 text-center">
        <h1 className="text-2xl font-semibold tracking-tight">Compare</h1>
        <p className="mt-2 text-sm text-ink2">
          Pick 2–4 businesses from search results using the compare toggle.
        </p>
      </div>
    )
  }

  return (
    <div className="container-page py-10">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Side by side</p>
          <h1 className="text-2xl font-semibold tracking-tight">Compare</h1>
        </div>
        <div className="flex items-center gap-2">
          <button onClick={() => void share()} className="flex items-center gap-1 text-sm text-ink3 hover:text-ink">
            <Link2 className="h-3.5 w-3.5" /> Copy link
          </button>
          <button onClick={() => { setIds([]); updateUrl() }} className="flex items-center gap-1 text-sm text-ink3 hover:text-ink">
            <Minus className="h-3.5 w-3.5" /> Clear
          </button>
        </div>
      </div>

      <div className="overflow-x-auto rounded-xl border border-border bg-surface shadow-card">
        <div className="p-4" style={{ minWidth: businesses.length * 220 + 140 }}>
          {/* Header */}
          <div className="grid gap-3 border-b border-border pb-4" style={{ gridTemplateColumns: `140px repeat(${businesses.length}, 1fr)` }}>
            <div />
            {businesses.map((b, i) => (
              <div
                key={b.id}
                draggable
                onDragStart={() => setDragIdx(i)}
                onDragOver={(e) => {
                  e.preventDefault()
                  overIdx.current = i
                }}
                onDrop={() => drop(i)}
                onDragEnd={() => setDragIdx(null)}
                className={`min-w-0 cursor-grab active:cursor-grabbing ${dragIdx === i ? 'opacity-40' : ''} ${overIdx.current === i && dragIdx !== null && dragIdx !== i ? 'ring-2 ring-ink/20' : ''}`}
                title="Drag to reorder"
              >
                <GripVertical className="h-3.5 w-3.5 text-ink3" />
                {b.logo_url ? (
                  <img src={b.logo_url} alt="" className="h-14 w-14 rounded-xl object-cover" />
                ) : (
                  <div className="flex h-14 w-14 items-center justify-center rounded-xl bg-surface2 text-lg font-semibold">{b.name.charAt(0)}</div>
                )}
                <a href={`/b/${b.slug}`} className="mt-2 block truncate text-sm font-semibold text-ink hover:underline">{b.name}</a>
                {b.verification_level && (
                  <Badge tone="attention" className="mt-1">{b.verification_level === 'fully_verified' ? 'Fully Verified' : 'Verified'}</Badge>
                )}
              </div>
            ))}
          </div>

          {row('Category', (b) => b.category_name ?? '—')}
          {row('Rating', (b) => b.review_count > 0 ? `★ ${b.rating_avg?.toFixed(1)} (${b.review_count})` : '—')}
          {row('Price level', (b) => "$".repeat(b.price_level ?? 0) || '—')}
          {row('Location', (b) => `${b.city}, ${b.country}`)}
          {row('Distance', (b) => (b.distance_km ? `${b.distance_km.toFixed(1)} km` : '—'))}
          {row('Open now', (b) => (b.is_open_now ? 'Yes' : 'No'))}
          {row('Hours', (b) => {
            const days = Object.entries(b.hours ?? {}).filter(([, h]) => h && !h.closed).length
            return `${days}/7 days`
          })}
          {row('Contact', (b) => b.contact?.phone ?? b.contact?.email ?? b.contact?.website ?? '—')}
          {row('Socials', (b) => {
            const count = ['instagram', 'tiktok', 'facebook', 'x', 'youtube', 'line', 'telegram'].filter((k) => b.contact?.[k]).length
            return count > 0 ? `${count} linked` : '—'
          })}
          {row('Engagement', (b) => `${b.like_count} likes · ${b.recommend_count} recs · ${b.save_count} saved`)}
        </div>
      </div>
    </div>
  )
}
