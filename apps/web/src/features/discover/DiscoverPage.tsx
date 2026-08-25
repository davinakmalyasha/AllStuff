import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { BellPlus, Search, SlidersHorizontal, X } from 'lucide-react'
import { api, searchPath, type BusinessDTO, type CategoryDTO } from '@/lib/api'
import { useDebouncedValue } from '@/lib/hooks'
import { BusinessCard } from '@/components/ui/BusinessCard'
import { Button } from '@/components/ui/Button'
import { PageSpinner, SkeletonCard, ErrorNote } from '@/components/ui/Spinner'
import { Modal } from '@/components/ui/Modal'
import { toast } from '@/components/ui/Toast'
import { usePageMeta } from '@/lib/meta'
import { useAuth } from '@/stores/auth'

type SortKey = 'trending' | 'rating' | 'newest' | 'nearest' | 'relevance'

export function DiscoverPage() {
  const { user } = useAuth()
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const [query, setQuery] = useState(q)
  const [showFilters, setShowFilters] = useState(false)
  const [sort, setSort] = useState<SortKey>((params.get('sort') as SortKey) ?? 'trending')
  const [cats, setCats] = useState<string[]>(params.getAll('category'))
  const [priceLevels, setPriceLevels] = useState<number[]>(params.getAll('price_level').map(Number))
  const [minRating, setMinRating] = useState(Number(params.get('min_rating') ?? 0) || 0)
  const [openNow, setOpenNow] = useState(params.get('open_now') === 'true')
  const [verifiedOnly, setVerifiedOnly] = useState(params.get('verified_only') === 'true')
  const [fullyVerified, setFullyVerified] = useState(params.get('fully_verified') === 'true')
  const [hasChat, setHasChat] = useState(params.get('has_chat') === 'true')
  const [coords, setCoords] = useState<{ lat: number; lng: number } | null>(null)
  const [suggestOpen, setSuggestOpen] = useState(false)
  const [submitted, setSubmitted] = useState(q)
  const [hl, setHl] = useState(-1)
  const inputRef = useRef<HTMLInputElement>(null)

  usePageMeta(submitted ? `Results for "${submitted}"` : 'Discover businesses')

  const { data: catData } = useQuery({
    queryKey: ['categories'],
    queryFn: () => api<{ categories: CategoryDTO[] }>('/categories'),
  })
  const leafCats = useMemo(() => {
    const out: CategoryDTO[] = []
    const walk = (cs: CategoryDTO[]) => {
      for (const c of cs) {
        if (c.children?.length) walk(c.children)
        else out.push(c)
      }
    }
    walk(catData?.categories ?? [])
    return out
  }, [catData])

  const debouncedQuery = useDebouncedValue(query, 250)
  const { data: suggest } = useQuery({
    queryKey: ['suggest', debouncedQuery],
    queryFn: () => api<{ businesses: Array<{ type: string; name: string; slug: string; category?: string; city?: string }>; categories: Array<{ type: string; name: string; slug: string; count: number }> }>(`/search/suggest?q=${encodeURIComponent(debouncedQuery)}`),
    enabled: debouncedQuery.trim().length >= 2 && document.activeElement === inputRef.current,
  })

  const apply = () => {
    const next = new URLSearchParams()
    if (submitted) next.set('q', submitted)
    for (const c of cats) next.append('category', c)
    for (const p of priceLevels) next.append('price_level', String(p))
    if (minRating) next.set('min_rating', String(minRating))
    if (openNow) next.set('open_now', 'true')
    if (verifiedOnly) next.set('verified_only', 'true')
    if (fullyVerified) next.set('fully_verified', 'true')
    if (hasChat) next.set('has_chat', 'true')
    if (coords) {
      next.set('lat', String(coords.lat))
      next.set('lng', String(coords.lng))
      next.set('sort', 'nearest')
    } else if (sort !== 'trending') {
      next.set('sort', sort)
    }
    setParams(next, { replace: true })
  }

  const nearMe = () => {
    if (!navigator.geolocation) {
      toast.error('Geolocation is not available.')
      return
    }
    navigator.geolocation.getCurrentPosition(
      (pos) => setCoords({ lat: pos.coords.latitude, lng: pos.coords.longitude }),
      () => toast.error('Could not get your location.'),
      { timeout: 8000 },
    )
  }

  // Load-more: both queries derive from ONE filter object — the old page-2
  // query silently dropped coords/radius/fully_verified and used a different
  // sort, so "Near me" became worldwide after the first 24 results.
  const [page, setPage] = useState(1)
  const [extras, setExtras] = useState<BusinessDTO[]>([])
  const searchFilters = {
    q: submitted || undefined, category: cats, price_level: priceLevels, min_rating: minRating,
    open_now: openNow, verified_only: verifiedOnly, fully_verified_only: fullyVerified,
    has_chat: hasChat, sort: coords ? ('nearest' as const) : sort, limit: 24,
    lat: coords?.lat, lng: coords?.lng, radius_km: coords ? 25 : undefined,
  }
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['search', submitted, cats, priceLevels, minRating, openNow, verifiedOnly, fullyVerified, hasChat, sort, coords],
    queryFn: () => api<{ businesses: BusinessDTO[]; count: number }>(searchPath(searchFilters)),
  })
  const { data: more, isFetching: moreLoading } = useQuery({
    queryKey: ['search-more', submitted, cats, priceLevels, minRating, openNow, verifiedOnly, fullyVerified, hasChat, sort, coords, page],
    queryFn: () => api<{ businesses: BusinessDTO[] }>(searchPath({ ...searchFilters, offset: page * 24 })),
    enabled: page > 1,
  })
  useEffect(() => {
    if (more?.businesses.length) {
      // Dedupe by id: trending reshuffles between pages otherwise produce
      // duplicate cards.
      setExtras((prev) => {
        const seen = new Set([...(data?.businesses ?? []).map((b) => b.id), ...prev.map((b) => b.id)])
        const fresh = more.businesses.filter((b) => !seen.has(b.id))
        return [...prev, ...fresh]
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [more])
  const filterKey = [submitted, cats.join(','), priceLevels.join(','), minRating, openNow, verifiedOnly, fullyVerified, hasChat, sort, coords?.lat ?? '', coords?.lng ?? ''].join('|')
  useEffect(() => {
    setPage(1)
    setExtras([])
  }, [filterKey])

  const all = [...(data?.businesses ?? []), ...extras]

  // Save search (Batch 2).
  const [saveOpen, setSaveOpen] = useState(false)
  const [saveName, setSaveName] = useState('')
  const [saveAlert, setSaveAlert] = useState(false)
  const saveSearch = async () => {
    const query: Record<string, unknown> = {}
    if (submitted) query.q = submitted
    if (cats.length) query.category = cats
    if (priceLevels.length) query.price_level = priceLevels
    if (minRating) query.min_rating = minRating
    if (openNow) query.open_now = true
    if (verifiedOnly) query.verified_only = true
    if (hasChat) query.has_chat = true
    try {
      await api('/me/saved-searches', { method: 'POST', body: { name: saveName, query, notify_daily: saveAlert } })
      setSaveOpen(false)
      setSaveName('')
      setSaveAlert(false)
      toast.success(saveAlert ? 'Search saved. Daily alerts enabled.' : 'Search saved. Check it in your profile.')
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  const submit = (text?: string) => {
    setSubmitted(text ?? query)
    setSuggestOpen(false)
    inputRef.current?.blur()
  }

  const toggleCat = (id: string) => setCats((prev) => (prev.includes(id) ? prev.filter((c) => c !== id) : [...prev, id]))
  const togglePrice = (n: number) => setPriceLevels((prev) => (prev.includes(n) ? prev.filter((p) => p !== n) : [...prev, n]))

  // Suggest options flattened for keyboard navigation (WAI-ARIA combobox).
  type SuggestOpt = { key: string; href: string; name: string; meta: string }
  const suggestOpts: SuggestOpt[] = suggest
    ? [
        ...suggest.businesses.map((s): SuggestOpt => ({ key: `b-${s.slug}`, href: `/b/${s.slug}`, name: s.name, meta: [s.category, s.city].filter(Boolean).join(' · ') })),
        ...suggest.categories.map((c): SuggestOpt => ({ key: `c-${c.slug}`, href: `/c/${c.slug}`, name: c.name, meta: `${c.count} businesses` })),
      ]
    : []

  const onSuggestKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (!suggestOpen || suggestOpts.length === 0) {
      if (e.key === 'Enter') submit()
      return
    }
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setHl((h) => (h + 1) % suggestOpts.length)
        break
      case 'ArrowUp':
        e.preventDefault()
        setHl((h) => (h <= 0 ? suggestOpts.length - 1 : h - 1))
        break
      case 'Enter':
        e.preventDefault()
        if (hl >= 0 && hl < suggestOpts.length) {
          window.location.assign(suggestOpts[hl].href)
        } else {
          submit()
        }
        break
      case 'Escape':
        setSuggestOpen(false)
        setHl(-1)
        inputRef.current?.blur()
        break
    }
  }

  const hasFilters = cats.length > 0 || priceLevels.length > 0 || minRating > 0 || openNow || verifiedOnly

  return (
    <div className="container-page py-8">
      <div className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="relative flex-1">
          <div className="flex items-center gap-2 rounded-xl border border-border bg-surface p-2 shadow-card">
            <Search className="ml-1 h-4 w-4 text-ink3" />
            <input
              ref={inputRef}
              value={query}
              onChange={(e) => {
                setQuery(e.target.value)
                setHl(-1)
                setSuggestOpen(true)
              }}
              onFocus={() => setSuggestOpen(true)}
              onBlur={() => setTimeout(() => setSuggestOpen(false), 200)}
              onKeyDown={onSuggestKeyDown}
              role="combobox"
              aria-expanded={suggestOpen && suggestOpts.length > 0}
              aria-controls="suggest-list"
              aria-activedescendant={hl >= 0 ? `suggest-opt-${hl}` : undefined}
              aria-autocomplete="list"
              placeholder="Search businesses, categories, cities…"
              className="h-9 w-full bg-transparent text-sm text-ink placeholder:text-ink3 focus:outline-none"
            />
            {query && (
              <button onClick={() => setQuery('')} className="text-ink3 hover:text-ink" aria-label="Clear">
                <X className="h-4 w-4" />
              </button>
            )}
            <Button size="sm" onClick={() => submit()}>Search</Button>
          </div>

          {suggestOpen && query.trim().length >= 2 && suggest && (
            <div id="suggest-list" role="listbox" aria-label="Suggestions" className="absolute z-30 mt-2 w-full overflow-hidden rounded-xl border border-border bg-surface shadow-cardHover">
              {suggestOpts.map((o, i) => (
                <a
                  key={o.key}
                  id={`suggest-opt-${i}`}
                  role="option"
                  aria-selected={i === hl}
                  href={o.href}
                  onMouseEnter={() => setHl(i)}
                  className={`flex items-center gap-3 px-4 py-2.5 text-sm ${i === hl ? 'bg-surface2' : ''}`}
                >
                  <span className="font-medium text-ink">{o.name}</span>
                  <span className="text-xs text-ink3">{o.meta}</span>
                </a>
              ))}
              {suggest.businesses.length === 0 && suggest.categories.length === 0 && (
                <p className="px-4 py-3 text-sm text-ink3">No matches. Try a broader search.</p>
              )}
            </div>
          )}
        </div>

        <div className="flex items-center gap-2">
          <select
            value={sort}
            onChange={(e) => setSort(e.target.value as SortKey)}
            className="h-10 rounded-lg border border-border bg-surface px-3 text-sm text-ink"
            aria-label="Sort"
          >
            <option value="trending">Trending</option>
            <option value="rating">Top rated</option>
            <option value="newest">Newest</option>
            <option value="nearest">Nearest</option>
            <option value="relevance">Relevance</option>
          </select>
          <Button variant={hasFilters ? 'primary' : 'secondary'} onClick={() => setShowFilters((v) => !v)}>
            <SlidersHorizontal className="h-4 w-4" /> Filters
          </Button>
        </div>
      </div>

      {showFilters && (
        <div className="mb-6 rounded-xl border border-border bg-surface p-4 shadow-card">
          <div className="grid gap-6 md:grid-cols-3">
            <div>
              <p className="mono-label mb-2">Category</p>
              <div className="max-h-48 space-y-1 overflow-y-auto pr-2">
                {leafCats.map((c) => (
                  <label key={c.id} className="flex items-center gap-2 text-sm text-ink2">
                    <input type="checkbox" checked={cats.includes(c.id)} onChange={() => toggleCat(c.id)} className="h-3.5 w-3.5 accent-black dark:accent-white" />
                    {c.name}
                  </label>
                ))}
              </div>
            </div>
            <div>
              <p className="mono-label mb-2">Price level</p>
              <div className="flex gap-2">
                {[1, 2, 3, 4].map((n) => (
                  <button
                    key={n}
                    onClick={() => togglePrice(n)}
                    className={`h-9 flex-1 rounded-lg border text-sm transition-colors ${
                      priceLevels.includes(n) ? 'border-ink bg-accent text-accent-ink' : 'border-border text-ink2 hover:bg-surface2'
                    }`}
                  >
                    {"$".repeat(n)}
                  </button>
                ))}
              </div>
              <p className="mono-label mb-2 mt-4">Minimum rating</p>
              <select value={minRating} onChange={(e) => setMinRating(Number(e.target.value))} className="h-9 w-full rounded-lg border border-border bg-surface px-2 text-sm text-ink">
                <option value={0}>Any</option>
                <option value={3.5}>3.5+</option>
                <option value={4}>4.0+</option>
                <option value={4.5}>4.5+</option>
              </select>
            </div>
            <div className="space-y-3">
              <p className="mono-label">Status</p>
              <label className="flex items-center gap-2 text-sm text-ink2">
                <input type="checkbox" checked={openNow} onChange={(e) => setOpenNow(e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" />
                Open now
              </label>
              <label className="flex items-center gap-2 text-sm text-ink2">
                <input type="checkbox" checked={verifiedOnly} onChange={(e) => setVerifiedOnly(e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" />
                Verified only
              </label>
              <label className="flex items-center gap-2 text-sm text-ink2">
                <input type="checkbox" checked={fullyVerified} onChange={(e) => setFullyVerified(e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" />
                Fully verified
              </label>
              <label className="flex items-center gap-2 text-sm text-ink2">
                <input type="checkbox" checked={hasChat} onChange={(e) => setHasChat(e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" />
                Replies to chat
              </label>
              {coords ? (
                <Button variant="secondary" size="sm" onClick={() => setCoords(null)}>Clear location</Button>
              ) : (
                <Button variant="secondary" size="sm" onClick={nearMe}>📍 Near me</Button>
              )}
              <Button size="sm" onClick={apply}>Apply filters</Button>
            </div>
          </div>
        </div>
      )}

      <p className="mb-4 flex items-center gap-3 text-sm text-ink3">
        {isLoading ? 'Searching…' : `${data?.count ?? 0} result${data?.count === 1 ? '' : 's'}${submitted ? ` for “${submitted}”` : ''}`}
        {user && (submitted || hasFilters) && (
          <button onClick={() => setSaveOpen(true)} className="flex items-center gap-1 text-ink3 hover:text-ink">
            <BellPlus className="h-3.5 w-3.5" /> Save search
          </button>
        )}
      </p>

      {isLoading ? (
        <PageSpinner />
      ) : isError ? (
        <ErrorNote message="Search is unavailable right now." onRetry={() => void refetch()} />
      ) : all.length ? (
        <>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {all.map((b) => <BusinessCard key={b.id} business={b} compare />)}
          </div>
          {moreLoading ? (
            <div className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              <SkeletonCard /><SkeletonCard /><SkeletonCard />
            </div>
          ) : all.length < (data?.count ?? 0) ? (
            <div className="mt-6 text-center">
              <Button variant="secondary" onClick={() => setPage((p) => p + 1)}>Load more</Button>
            </div>
          ) : null}
        </>
      ) : (
        <div className="card flex flex-col items-center justify-center gap-2 py-16 text-center">
          <p className="font-mono text-sm text-ink3">No results</p>
          <p className="text-sm text-ink2">Try clearing filters or searching something broader.</p>
          {!hasFilters && (
            <>
              <div className="mt-2 flex flex-wrap justify-center gap-1.5">
                {leafCats.slice(0, 6).map((c) => (
                  <Link key={c.id} to={`/c/${c.slug}`} className="rounded-full border border-border px-3 py-1 text-xs text-ink2 hover:bg-surface2">
                    {c.name}
                  </Link>
                ))}
              </div>
              <Link to="/dashboard/register" className="mt-2 text-sm font-medium text-ink underline underline-offset-4 hover:text-ink2">
                Add your business →
              </Link>
            </>
          )}
          {hasFilters && <Button variant="secondary" size="sm" className="mt-2" onClick={() => { setCats([]); setPriceLevels([]); setMinRating(0); setOpenNow(false); setVerifiedOnly(false); setFullyVerified(false); }}>Clear filters</Button>}
        </div>
      )}

      {/* Save search */}
      <Modal open={saveOpen} onClose={() => setSaveOpen(false)} title="Save this search">
        <div className="space-y-3">
          <p className="text-sm text-ink2">We'll keep this search handy in your profile — and it powers future alerts.</p>
          <input value={saveName} onChange={(e) => setSaveName(e.target.value)} placeholder="e.g. Cafés near me" className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink" autoFocus />
          <label className="flex items-center gap-2 text-sm text-ink2">
            <input type="checkbox" checked={saveAlert} onChange={(e) => setSaveAlert(e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" />
            Email me daily when new businesses match
          </label>
          <div className="flex justify-end">
            <Button onClick={() => void saveSearch()} disabled={!saveName.trim()}>Save</Button>
          </div>
        </div>
      </Modal>
    </div>
  )
}
