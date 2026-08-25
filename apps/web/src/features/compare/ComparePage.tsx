import { useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { GripVertical, Minus, Link2, MessageSquare, X } from 'lucide-react'
import { api, type BusinessDTO, type ProductDTO } from '@/lib/api'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner } from '@/components/ui/Spinner'
import { usePageMeta } from '@/lib/meta'
import { useCompare } from '@/stores/compare'
import { useAuth } from '@/stores/auth'
import { formatMoney, useCurrency } from '@/stores/currency'

/** Side-by-side comparison (PRD §5.1.5, §6.1 /compare?b=…). URL is shareable. */
export function ComparePage() {
  const [params, setParams] = useSearchParams()
  const navigate = useNavigate()
  const { user } = useAuth()
  const { rates, display } = useCurrency()
  const { ids, setIds } = useCompare()
  const [dragIdx, setDragIdx] = useState<number | null>(null)
  const overIdx = useRef<number | null>(null)

  const queryIds = (params.get('b') ?? '').split(',').filter(Boolean)
  const activeIds = queryIds.length >= 2 ? queryIds : ids

  usePageMeta('Compare businesses')

  const { data, isLoading } = useQuery({
    queryKey: ['compare', activeIds.join(',')],
    queryFn: () => api<{ businesses: BusinessDTO[]; top_products: Record<string, ProductDTO[]> }>(`/compare?b=${activeIds.join(',')}`),
    enabled: activeIds.length >= 2,
  })

  const businesses = data?.businesses ?? []
  const topProducts = data?.top_products ?? {}

  // URL is the source of truth while viewing a shared ≥2-id selection;
  // store mutations then would push someone else's picks into your tray.
  const isSharedView = queryIds.length >= 2

  const setUrlIds = (nextIds: string[]) => {
    const url = new URLSearchParams(params)
    if (nextIds.length >= 2) url.set('b', nextIds.join(','))
    else url.delete('b')
    setParams(url, { replace: true })
  }

  const removeColumn = (id: string) => {
    const remaining = activeIds.filter((x) => x !== id)
    if (isSharedView) {
      // Removing a column from a SHARED link must not add that business to
      // your persistent tray (the old toggle() did exactly that).
      setUrlIds(remaining)
      return
    }
    const next = ids.filter((x) => x !== id)
    setIds(next)
    setUrlIds(next)
  }

  const chat = async (businessId: string) => {
    try {
      const r = await api<{ thread: { id: string } }>('/threads', { method: 'POST', body: { business_id: businessId } })
      navigate(`/me/messages/${r.thread.id}`)
    } catch {
      /* thread may already exist for this business+user pair; server dedupes */
    }
  }

  const share = async () => {
    const url = `${window.location.origin}/compare?b=${activeIds.join(',')}`
    try {
      await navigator.clipboard.writeText(url)
    } catch {
      /* noop */
    }
  }

  const clearAll = () => {
    // Compute the empty state EXPLICITLY — the old `setIds([]); updateUrl()`
    // read the pre-clear closure and wrote the old ids back into the URL, so
    // reloading resurrected the comparison.
    setIds([])
    setUrlIds([])
  }

  // Drag a column header to reorder (updates the shareable URL).
  const drop = (to: number) => {
    setDragIdx(null)
    overIdx.current = null
    if (dragIdx === null || dragIdx === to) return
    const next = [...activeIds]
    const [moved] = next.splice(dragIdx, 1)
    next.splice(to, 0, moved)
    if (isSharedView) {
      setUrlIds(next)
      return
    }
    setIds(next)
    setUrlIds(next)
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
          <button onClick={clearAll} className="flex items-center gap-1 text-sm text-ink3 hover:text-ink">
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
                <div className="flex items-start justify-between">
                  <GripVertical className="h-3.5 w-3.5 text-ink3" />
                  <button onClick={() => removeColumn(b.id)} className="rounded p-0.5 text-ink3 hover:bg-surface2 hover:text-ink" aria-label={`Remove ${b.name} from compare`} title="Remove">
                    <X className="h-3.5 w-3.5" />
                  </button>
                </div>
                {b.logo_url ? (
                  <img src={b.logo_url} alt="" className="h-14 w-14 rounded-xl object-cover" />
                ) : (
                  <div className="flex h-14 w-14 items-center justify-center rounded-xl bg-surface2 text-lg font-semibold">{b.name.charAt(0)}</div>
                )}
                <Link to={`/b/${b.slug}`} className="mt-2 block truncate text-sm font-semibold text-ink hover:underline">{b.name}</Link>
                {b.verification_level && (
                  <Badge tone="attention" className="mt-1">{b.verification_level === 'fully_verified' ? 'Fully Verified' : 'Verified'}</Badge>
                )}
              </div>
            ))}
          </div>

          {row('Category', (b) => b.category_name ?? '—')}
          {row('Rating', (b) => b.review_count > 0 ? `★ ${b.rating_avg?.toFixed(1)} (${b.review_count})` : '—')}
          {row('Price level', (b) => "$".repeat(b.price_level ?? 0) || '—')}
          {row('Top products', (b) => {
            const list = topProducts[b.id] ?? []
            if (!list.length) return '—'
            return (
              <ul className="space-y-1">
                {list.map((p) => (
                  <li key={p.id} className="truncate">
                    {p.name}
                    <span className="ml-1 font-mono text-xs text-ink3">
                      {p.call_for_price || p.base_price == null ? 'ask' : formatMoney(p.base_price, p.currency ?? b.currency, display, rates)}
                    </span>
                  </li>
                ))}
              </ul>
            )
          })}
          {row('Amenities', (b) => {
            const chips = [...(b.amenities ?? []), ...(b.tags ?? [])].slice(0, 6)
            if (!chips.length) return '—'
            return (
              <div className="flex flex-wrap gap-1">
                {chips.map((a) => <Badge key={a}>{a}</Badge>)}
              </div>
            )
          })}
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
          {row('Actions', (b) => (
            <div className="flex flex-wrap gap-2">
              {user ? (
                <button onClick={() => void chat(b.id)} className="flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-xs text-ink hover:bg-surface2">
                  <MessageSquare className="h-3 w-3" /> Chat
                </button>
              ) : (
                <Link to={`/login?next=/b/${b.slug}`} className="rounded-lg border border-border px-2 py-1 text-xs text-ink2">Log in to chat</Link>
              )}
              <Link to={`/b/${b.slug}`} className="rounded-lg border border-border px-2 py-1 text-xs text-ink hover:bg-surface2">View</Link>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
