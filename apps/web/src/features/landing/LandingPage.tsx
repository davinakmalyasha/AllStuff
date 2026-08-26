import { useMemo, useState } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import {
  ArrowRight,

  Compass,
  MapPin,
  MessageSquare,
  Search,
  ShieldCheck,
  Sparkles,
  Store,
  TrendingUp,
} from 'lucide-react'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { api, type BusinessDTO, type CategoryDTO, type TrendEntryDTO } from '@/lib/api'
import { usePageMeta } from '@/lib/meta'
import { useAuth } from '@/stores/auth'
import { categoryIcon } from '@/components/ui/CategoryIcon'
import { HomeMapWidget } from '@/features/map/HomeMapWidget'

const FALLBACK_CATEGORIES: { name: string; note: string; slug: string; icon: string }[] = [
  { name: 'Food', note: 'Street food to fine dining', slug: 'food', icon: 'utensils' },
  { name: 'Café', note: 'Coffee, tea, and everything between', slug: 'cafe', icon: 'coffee' },
  { name: 'Restaurant', note: 'Where the city eats', slug: 'restaurant', icon: 'chef-hat' },
  { name: 'Photobooth', note: 'Moments, captured', slug: 'photobooth', icon: 'camera' },
  { name: 'Salon', note: 'Cut, color, care', slug: 'salon', icon: 'scissors' },
  { name: 'Barber', note: 'Sharp looks', slug: 'barber', icon: 'scissors' },
  { name: 'Fashion', note: 'Clothes that fit your style', slug: 'fashion', icon: 'shirt' },
  { name: 'Workshop', note: 'Hands-on, handcrafted', slug: 'crafts-hobby', icon: 'palette' },
  { name: 'Studio', note: 'Music, art, movement', slug: 'photography-studio', icon: 'aperture' },
  { name: 'Services', note: 'Everything else, everywhere', slug: 'services', icon: 'wrench' },
]


// Icons only — titles/texts come from i18n at render time (landing.howStep*).
const STEPS = [
  { icon: Compass, titleKey: 'landing.howStep1Title', textKey: 'landing.howStep1Text' },
  { icon: TrendingUp, titleKey: 'landing.howStep2Title', textKey: 'landing.howStep2Text' },
  { icon: Store, titleKey: 'landing.howStep3Title', textKey: 'landing.howStep3Text' },
]

// Icons only — titles/texts come from i18n at render time (landing.feature*).
const FEATURES = [
  { icon: Search, titleKey: 'landing.featureSearchTitle', textKey: 'landing.featureSearchText' },
  { icon: TrendingUp, titleKey: 'landing.featureTrendingTitle', textKey: 'landing.featureTrendingText' },
  { icon: Store, titleKey: 'landing.featureStorefrontTitle', textKey: 'landing.featureStorefrontText' },
  { icon: MessageSquare, titleKey: 'landing.featureChatTitle', textKey: 'landing.featureChatText' },
  { icon: ShieldCheck, titleKey: 'landing.featureVerificationTitle', textKey: 'landing.featureVerificationText' },
  { icon: MapPin, titleKey: 'landing.featureCategoriesTitle', textKey: 'landing.featureCategoriesText' },
]

function Stat({ value, label }: { value: string; label: string }) {
  return (
    <div className="flex flex-col items-center gap-1 text-center">
      <span className="font-mono text-2xl font-semibold tracking-tight text-ink sm:text-3xl">{value}</span>
      <span className="text-xs uppercase tracking-wider text-ink3">{label}</span>
    </div>
  )
}

export function LandingPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [query, setQuery] = useState('')

  usePageMeta('', undefined, { url: '/' })

  const { data: catData } = useQuery({
    queryKey: ['categories'],
    queryFn: () => api<{ categories: CategoryDTO[] }>('/categories'),
  })
  const topCategories = useMemo(() => {
    const top = (catData?.categories ?? []).slice(0, 10)
    return top.length > 0 ? top.map((c) => ({ name: c.name, note: `${c.count} business${c.count === 1 ? '' : 'es'}`, slug: c.slug, icon: c.icon })) : FALLBACK_CATEGORIES
  }, [catData])

  const { data: trendingData } = useQuery({
    queryKey: ['trending'],
    queryFn: () => api<{ entries: TrendEntryDTO[] }>('/trending'),
    refetchInterval: 5 * 60_000,
  })
  const { data: risingData } = useQuery({
    queryKey: ['rising'],
    queryFn: () => api<{ entries: TrendEntryDTO[] }>('/rising'),
    refetchInterval: 5 * 60_000,
  })
  const { data: featuredData } = useQuery({
    queryKey: ['featured'],
    queryFn: () => api<{ businesses: BusinessDTO[] }>('/featured'),
  })
  const { user } = useAuth()
  const { data: followFeed } = useQuery({
    queryKey: ['following-feed'],
    queryFn: () => api<{ updates: Array<{ id: string; business_id: string; title: string; body: string; created_at: string; business_name?: string; business_slug?: string }> }>('/me/following-feed?limit=4'),
    enabled: !!user,
  })
  const trending = (trendingData?.entries ?? []).map((e, i) => ({
    rank: i + 1, name: e.name, cat: e.category ?? e.city, score: e.score.toFixed(1),
    delta: e.is_booming ? `▲ ${e.velocity.toFixed(1)}` : '', rising: e.is_rising, slug: e.slug,
  }))
  const rising = risingData?.entries ?? []

  const submitSearch = (e: React.FormEvent) => {
    e.preventDefault()
    navigate(query ? `/discover?q=${encodeURIComponent(query)}` : '/discover')
  }

  return (
    <div className="animate-fadeUp">
      {/* Hero */}
      <section className="relative overflow-hidden">
        <div
          className="pointer-events-none absolute inset-0 opacity-[0.035] dark:opacity-[0.05]"
          style={{
            backgroundImage:
              'linear-gradient(var(--color-ink) 1px, transparent 1px), linear-gradient(90deg, var(--color-ink) 1px, transparent 1px)',
            backgroundSize: '48px 48px',
          }}
          aria-hidden
        />
        <div className="container-page relative py-20 text-center sm:py-28">
          <div className="mx-auto mb-6 flex justify-center">
            <Badge tone="attention">{t('landing.badge')}</Badge>
          </div>
          <h1 className="mx-auto max-w-3xl text-4xl font-semibold tracking-tight text-ink sm:text-6xl">
            {t('landing.headline')}
          </h1>
          <p className="mx-auto mt-5 max-w-xl text-base text-ink2 sm:text-lg">
            {t('landing.subheadline')}
          </p>

          <form
            onSubmit={submitSearch}
            className="mx-auto mt-10 flex max-w-xl items-center gap-2 rounded-xl border border-border bg-surface p-2 shadow-card"
            role="search"
          >
            <Search className="ml-2 h-4 w-4 shrink-0 text-ink3" aria-hidden />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('landing.searchPlaceholder')}
              className="h-9 w-full bg-transparent text-sm text-ink placeholder:text-ink3 focus:outline-none"
              aria-label={t('landing.searchPlaceholder')}
            />
            <Button size="sm" type="submit" className="shrink-0">
              {t('common.submit')}
            </Button>
          </form>

          <div className="mx-auto mt-16 grid max-w-2xl grid-cols-2 gap-8 sm:grid-cols-4">
            <Stat value="100+" label={t('landing.stats.categories')} />
            <Stat value="100%" label={t('landing.stats.free')} />
            <Stat value="2" label={t('landing.stats.verified')} />
            <Stat value="∞" label={t('landing.stats.messages')} />
          </div>
        </div>
      </section>

      {/* Categories — live from the directory (PRD §5.1.1) */}
      <section className="border-t border-border py-16 sm:py-20">
        <div className="container-page">
          <div className="mb-10 text-center">
            <p className="mono-label mb-2">Directory</p>
            <h2 className="text-2xl font-semibold tracking-tight sm:text-3xl">{t('landing.categoriesHeading')}</h2>
            <p className="mt-2 text-sm text-ink2">{t('landing.categoriesSub')}</p>
          </div>
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
            {topCategories.map((c) => (
              <button
                key={c.name}
                onClick={() => navigate(`/discover?q=${encodeURIComponent(c.name)}`)}
                className="group card flex flex-col items-start gap-3 p-4 text-left transition-all duration-200 hover:-translate-y-0.5 hover:shadow-cardHover"
              >
                <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-surface2 text-ink transition-colors group-hover:bg-accent group-hover:text-accent-ink">
                  {(() => { const Icon = categoryIcon(c.icon); return <Icon className="h-4 w-4" aria-hidden /> })()}
                </span>
                <span>
                  <span className="block text-sm font-medium text-ink">{c.name}</span>
                  <span className="mt-0.5 block text-xs text-ink3">{c.note}</span>
                </span>
              </button>
            ))}
          </div>
        </div>
      </section>

      {/* Trending + Rising */}
      <section className="border-t border-border bg-surface/50 py-16 sm:py-20">
        <div className="container-page grid gap-8 lg:grid-cols-2">
          <div>
            <div className="mb-6">
              <p className="mono-label mb-2">Live</p>
              <h2 className="text-xl font-semibold tracking-tight">{t('landing.trending')}</h2>
            </div>
            <Card className="divide-y divide-border p-0">
              {trending.length === 0 && <p className="px-5 py-6 text-center text-sm text-ink3">Signals are warming up — leaderboards fill as people engage.</p>}
              {trending.map((row) => (
                <button
                  key={row.rank}
                  onClick={() => navigate(`/b/${row.slug}`)}
                  className="flex w-full items-center gap-4 px-5 py-4 text-left transition-colors hover:bg-surface2"
                >
                  <span className="font-mono text-sm text-ink3 w-6">{row.rank}</span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-ink">{row.name}</p>
                    <p className="text-xs text-ink3">{row.cat}</p>
                  </div>
                  {row.rising && <Badge tone="attention">Rising</Badge>}
                  {row.delta && <span className="font-mono text-xs text-ink3">{row.delta}</span>}
                  <span className="font-mono text-sm text-ink">{row.score}</span>
                </button>
              ))}
            </Card>
          </div>

          <div className="flex flex-col">
            <div className="mb-6 flex items-center justify-between">
              <div>
                <p className="mono-label mb-2">Fairness engine</p>
                <h2 className="text-xl font-semibold tracking-tight">{t('landing.rising')}</h2>
              </div>
              <Sparkles className="h-4 w-4 text-ink3" aria-hidden />
            </div>
            <Card hover className="flex flex-1 flex-col justify-center gap-4">
              <p className="text-sm leading-relaxed text-ink2">
                Leaderboards reward real engagement — but new and small businesses get a
                velocity-based <span className="font-medium text-ink">Rising tier</span> with
                guaranteed visibility. Hidden gems surface instead of being buried.
              </p>
              {rising.length > 0 && (
                <div className="space-y-1.5">
                  {rising.map((e, i) => (
                    <button
                      key={e.id}
                      onClick={() => navigate(`/b/${e.slug}`)}
                      className="flex w-full items-center gap-3 rounded-lg px-2 py-1.5 text-left text-sm transition-colors hover:bg-surface2"
                    >
                      <span className="font-mono text-xs text-ink3">{i + 1}</span>
                      <span className="min-w-0 flex-1 truncate font-medium text-ink">{e.name}</span>
                      <Badge tone="attention" dot>Rising</Badge>
                      <span className="font-mono text-xs text-ink3">{e.velocity.toFixed(1)}</span>
                    </button>
                  ))}
                </div>
              )}
              <ul className="space-y-2 text-sm text-ink2">
                <li className="flex items-start gap-2">
                  <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-ink" aria-hidden />
                  Time-weighted scores across 24h / 7d / 30d windows
                </li>
                <li className="flex items-start gap-2">
                  <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-ink" aria-hidden />
                  Global, per-category, and per-city leaderboards
                </li>
                <li className="flex items-start gap-2">
                  <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-ink" aria-hidden />
                  Anti-gaming: deduped views, anomaly flags, capped contributions
                </li>
              </ul>
              <Button variant="secondary" className="mt-2 self-start" onClick={() => navigate('/discover')}>
                See the leaderboard <ArrowRight className="h-4 w-4" />
              </Button>
            </Card>
          </div>
        </div>
      </section>

      {/* How it works */}
      <section className="py-16 sm:py-20">
        <div className="container-page">
          <div className="mb-10 text-center">
            <p className="mono-label mb-2">Three steps</p>
            <h2 className="text-2xl font-semibold tracking-tight sm:text-3xl">{t('landing.howHeading')}</h2>
          </div>
          <div className="grid gap-6 md:grid-cols-3">
            {STEPS.map((s, i) => (
              <Card key={s.titleKey} hover className="relative">
                <span className="mono-label absolute right-5 top-5">0{i + 1}</span>
                <s.icon className="h-5 w-5 text-ink" aria-hidden />
                <h3 className="mt-4 text-base font-semibold text-ink">{t(s.titleKey)}</h3>
                <p className="mt-2 text-sm leading-relaxed text-ink2">{t(s.textKey)}</p>
              </Card>
            ))}
          </div>
        </div>
      </section>

      {/* Features */}
      <section className="border-t border-border bg-surface/50 py-16 sm:py-20">
        <div className="container-page">
          <div className="mb-10 text-center">
            <p className="mono-label mb-2">Why BizVerse</p>
            <h2 className="text-2xl font-semibold tracking-tight sm:text-3xl">{t('landing.featuresHeading')}</h2>
          </div>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {FEATURES.map((f) => (
              <Card key={f.titleKey} hover>
                <f.icon className="h-5 w-5 text-ink" aria-hidden />
                <h3 className="mt-4 text-sm font-semibold text-ink">{t(f.titleKey)}</h3>
                <p className="mt-1.5 text-sm leading-relaxed text-ink2">{t(f.textKey)}</p>
              </Card>
            ))}
          </div>
        </div>
      </section>

      {/* Following feed */}
      {(followFeed?.updates?.length ?? 0) > 0 && (
        <section className="border-t border-border bg-surface/50 py-16 sm:py-20">
          <div className="container-page">
            <div className="mb-6 flex items-center justify-between">
              <p className="mono-label">From businesses you follow</p>
              <Link to="/me/following" className="text-sm text-ink underline underline-offset-4 hover:text-ink2">See all</Link>
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              {(followFeed?.updates ?? []).map((u) => (
                <Link key={u.id} to={`/b/${u.business_slug}`} className="card p-4 transition-shadow hover:shadow-cardHover">
                  <p className="text-xs text-ink3">{u.business_name}</p>
                  <p className="mt-1 text-sm font-semibold text-ink">{u.title}</p>
                  <p className="mt-1 line-clamp-2 text-sm text-ink2">{u.body}</p>
                </Link>
              ))}
            </div>
          </div>
        </section>
      )}

      {/* Featured */}
      {(featuredData?.businesses ?? []).length > 0 ? (
        <section className="border-t border-border py-16 sm:py-20">
          <div className="container-page">
            <p className="mono-label mb-6 text-center">Hand-picked</p>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              {(featuredData?.businesses ?? []).map((b) => (
                <Link key={b.id} to={`/b/${b.slug}`} className="card group overflow-hidden transition-shadow hover:shadow-cardHover">
                  {b.cover_url ? (
                    <img src={b.cover_url} alt="" className="h-28 w-full object-cover" loading="lazy" />
                  ) : (
                    <div className="flex h-28 items-center justify-center bg-surface2 text-2xl font-semibold text-ink2">{b.name.charAt(0)}</div>
                  )}
                  <div className="p-4">
                    <p className="truncate text-sm font-semibold text-ink group-hover:underline">{b.name}</p>
                    <p className="text-xs text-ink3">{b.category_name} · {b.city}</p>
                    {b.review_count > 0 && <p className="mt-1 text-xs text-ink2">★ {b.rating_avg?.toFixed(1)} ({b.review_count})</p>}
                  </div>
                </Link>
              ))}
            </div>
          </div>
        </section>
      ) : null}

      {/* Map */}
      <HomeMapWidget />

      {/* Owner CTA */}
      <section className="border-t border-border py-16 sm:py-24">
        <div className="container-page">
          <div className="card mx-auto max-w-3xl p-8 text-center sm:p-12">
            <p className="mono-label mb-3">Free forever</p>
            <h2 className="text-2xl font-semibold tracking-tight sm:text-3xl">{t('landing.ownerCtaTitle')}</h2>
            <p className="mx-auto mt-3 max-w-md text-sm text-ink2 sm:text-base">{t('landing.ownerCtaText')}</p>
            <Button size="lg" className="mt-8" onClick={() => navigate('/register')}>
              {t('landing.ownerCtaButton')} <ArrowRight className="h-4 w-4" />
            </Button>
          </div>
        </div>
      </section>
    </div>
  )
}
