import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Clock,
  Flame,
  Globe,
  Mail,
  MapPin,
  MessageSquare,
  Phone,
  ShieldCheck,
  Star,
  TrendingUp,
} from 'lucide-react'
import { api, type BusinessDTO, type ProductDTO } from '@/lib/api'
import { safeExternalUrl } from '@/lib/url'
import { MiniMapLive } from '@/components/map/MiniMapLive'
import { ProductModal } from '@/components/engagement/ProductModal'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { PageSpinner, ErrorNote } from '@/components/ui/Spinner'
import { usePageMeta, useJsonLd } from '@/lib/meta'
import { useAuth } from '@/stores/auth'
import { priceLabel } from '@/features/dashboard/StorefrontPreview'
import { EngagementBar } from '@/components/engagement/EngagementBar'
import { ReviewsSection } from '@/components/engagement/ReviewsSection'
import { CommentsSection } from '@/components/engagement/CommentsSection'
import { QASection } from '@/components/engagement/QASection'
import { UpdatesSection } from '@/components/engagement/UpdatesSection'
import { ShareButton, ReportButton } from '@/components/engagement/ReportShare'

const SOCIALS: Record<string, string> = {
  whatsapp: 'WhatsApp', instagram: 'Instagram', tiktok: 'TikTok', facebook: 'Facebook',
  x: 'X', youtube: 'YouTube', line: 'Line', telegram: 'Telegram',
}

const FONTS: Record<string, string> = {
  inter: "'Inter', system-ui, sans-serif",
  serif: 'Georgia, "Times New Roman", serif',
  mono: 'ui-monospace, SFMono-Regular, Menlo, monospace',
}

export function BusinessPage() {
  const { slug = '' } = useParams()
  const { user } = useAuth()
  const [product, setProduct] = useState<ProductDTO | null>(null)

  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: ['business', slug],
    queryFn: () =>
      api<{ business: BusinessDTO; similar: BusinessDTO[]; products: ProductDTO[]; is_owner: boolean; trend?: { is_booming: boolean; is_rising: boolean }; preview?: { theme?: { colors?: Record<string, string>; font?: string }; layout?: { sections?: { key: string; enabled: boolean }[]; highlights?: { icon: string; title: string; text: string }[] } } }>(`/b/${slug}${window.location.search}`),
  })

  const b = data?.business
  usePageMeta(b ? `${b.name} — ${b.category_name ?? 'Business'}` : 'Business', b?.tagline ?? b?.description, {
    image: b ? `/og/b/${b.slug}` : undefined,
    url: b ? `${window.location.origin}/b/${b.slug}` : undefined,
  })
  useJsonLd(
    b
      ? {
          '@context': 'https://schema.org',
          '@type': 'LocalBusiness',
          name: b.name,
          description: b.description,
          image: b.logo_url ?? undefined,
          address: { '@type': 'PostalAddress', streetAddress: b.address, addressLocality: b.city, addressCountry: b.country },
          openingHours: Object.entries(b.hours)
            .filter(([, h]) => h && !h.closed)
            .map(([d, h]) => `${capitalize(d)} ${h?.open}-${h?.close}`),
          aggregateRating:
            b.review_count > 0
              ? { '@type': 'AggregateRating', ratingValue: b.rating_avg, reviewCount: b.review_count }
              : undefined,
        }
      : null,
  )

  if (isLoading) return <PageSpinner />
  if (isError && (error as { status?: number })?.status !== 404) {
    return <div className="container-page py-10"><ErrorNote message="This page failed to load." onRetry={() => void refetch()} /></div>
  }
  if (!b) return (
    <div className="container-page flex min-h-[50vh] flex-col items-center justify-center gap-2 text-center">
      <p className="font-mono text-5xl font-semibold tracking-tight">404</p>
      <p className="text-sm text-ink2">This business doesn't exist or isn't public yet.</p>
    </div>
  )

  // Storefront theming from the published snapshot (PRD §10.5): drafts never public.
  // With ?draft=1 the owner sees their draft via the `preview` payload.
  const preview = data?.preview as { theme?: { colors?: Record<string, string>; font?: string }; layout?: { sections?: { key: string; enabled: boolean }[]; highlights?: { icon: string; title: string; text: string }[] } } | undefined
  const snapshot = preview ?? (b as unknown as { published_snapshot?: { theme?: { colors?: Record<string, string>; font?: string }; layout?: { sections?: { key: string; enabled: boolean }[]; highlights?: { icon: string; title: string; text: string }[] } } }).published_snapshot
  const theme = snapshot?.theme
  const layout = snapshot?.layout
  const colors = theme?.colors ?? {}
  const enabled = (key: string) => layout?.sections?.find((s) => s.key === key)?.enabled ?? true
  const vars = {
    '--pv-bg': colors.bg ?? '#ffffff',
    '--pv-surface': colors.surface ?? '#fafafa',
    '--pv-ink': colors.ink ?? '#18181b',
    '--pv-accent': colors.accent ?? '#18181b',
    '--pv-muted': colors.muted ?? '#71717a',
    '--pv-font': FONTS[theme?.font ?? 'inter'],
  } as React.CSSProperties

  const openDays = Object.entries(b.hours).filter(([, h]) => h && !h.closed)
  const products = data?.products ?? []

  return (
    <div style={{ ...vars, background: 'var(--pv-bg)', color: 'var(--pv-ink)', fontFamily: 'var(--pv-font)' }}>
      {/* Hero */}
      <div className="h-48 w-full sm:h-64" style={{ background: 'var(--pv-surface)' }} >
        {b.cover_url && <img src={b.cover_url} alt="" className="h-full w-full object-cover" />}
      </div>
      <div className="mx-auto w-full max-w-6xl px-4 sm:px-6 lg:px-8">
        <div className="-mt-10 flex flex-col gap-4 sm:flex-row sm:items-end">
          {b.logo_url ? (
            <img src={b.logo_url} alt={`${b.name} logo`} className="h-20 w-20 rounded-2xl border border-border object-cover" style={{ background: 'var(--pv-surface)', boxShadow: '0 4px 24px rgb(0 0 0 / 0.14)' }} />
          ) : (
            <div className="flex h-20 w-20 items-center justify-center rounded-2xl border border-border text-2xl font-semibold" style={{ background: 'var(--pv-surface)' }}>{b.name.charAt(0)}</div>
          )}
          <div className="min-w-0 flex-1 pb-1">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">{b.name}</h1>
              {b.verification_level && (
                <Badge tone="attention"><ShieldCheck className="h-3 w-3" /> {b.verification_level === 'fully_verified' ? 'Fully Verified' : 'Verified'}</Badge>
              )}
              {data?.trend?.is_booming && <Badge tone="positive"><Flame className="h-3 w-3" /> Booming</Badge>}
              {!data?.trend?.is_booming && data?.trend?.is_rising && <Badge tone="attention"><TrendingUp className="h-3 w-3" /> Rising</Badge>}
            </div>
            {b.tagline && <p className="mt-1 text-sm" style={{ color: 'var(--pv-muted)' }}>{b.tagline}</p>}
            <div className="mt-1.5 flex flex-wrap items-center gap-3 text-sm" style={{ color: 'var(--pv-muted)' }}>
              <span className="font-medium" style={{ color: 'var(--pv-ink)' }}>{b.category_name}</span>
              {b.review_count > 0 && (
                <span className="flex items-center gap-1"><Star className="h-3.5 w-3.5" /> {b.rating_avg?.toFixed(1)} ({b.review_count})</span>
              )}
              {b.founded_year != null && <span className="font-mono text-xs">Est. {b.founded_year}</span>}
              <span>{b.city}, {b.country}</span>
              <span className="font-mono text-xs">{"$".repeat(b.price_level ?? 0) || '—'}</span>
              <Badge tone={b.is_open_now ? 'positive' : 'neutral'} dot>{b.is_open_now ? 'Open now' : 'Closed now'}</Badge>
            </div>
          </div>
          <div className="flex gap-2 pb-1">
            <EngagementBar
              businessId={b.id}
              businessSlug={b.slug}
              counts={{ likes: b.like_count, recommends: b.recommend_count, saves: b.save_count }}
            />
            <FollowButton businessId={b.id} loggedIn={!!user} />
            <ShareButton slug={b.slug} name={b.name} />
            <ReportButton targetType="business" targetId={b.id} />
            <MessageButton businessId={b.id} loggedIn={!!user} slug={b.slug} />
          </div>
        </div>
      </div>

      <div className="mx-auto grid w-full max-w-6xl gap-8 px-4 py-10 sm:px-6 lg:grid-cols-3 lg:px-8">
        <div className="space-y-6 lg:col-span-2">
          {enabled('about') && (
            <section>
              <h2 className="mono-label mb-3">About</h2>
              <p className="text-sm leading-relaxed" style={{ color: 'var(--pv-muted)' }}>{b.description}</p>
              {b.tags.length > 0 && (
                <div className="mt-3 flex flex-wrap gap-2">
                  {b.tags.map((t) => <Badge key={t}>#{t}</Badge>)}
                </div>
              )}
            </section>
          )}

          {enabled('highlights') && (layout?.highlights?.length ?? 0) > 0 && (
            <section>
              <h2 className="mono-label mb-3">Highlights</h2>
              <div className="grid gap-3 sm:grid-cols-3">
                {layout!.highlights!.map((h, i) => (
                  <div key={i} className="rounded-xl p-4" style={{ background: 'var(--pv-surface)' }}>
                    <p className="text-sm font-semibold">{h.title}</p>
                    <p className="mt-1 text-xs" style={{ color: 'var(--pv-muted)' }}>{h.text}</p>
                  </div>
                ))}
              </div>
            </section>
          )}

          {(b.amenities?.length ?? 0) > 0 && (
            <section>
              <h2 className="mono-label mb-3">Amenities</h2>
              <div className="flex flex-wrap gap-2">
                {b.amenities!.map((a) => (
                  <span key={a} className="rounded-full border border-border bg-surface px-3 py-1 text-xs text-ink2">✓ {a}</span>
                ))}
              </div>
            </section>
          )}

          {(b.gallery?.length ?? 0) > 0 && (
            <section>
              <h2 className="mono-label mb-3">Gallery</h2>
              <div className="grid grid-cols-3 gap-2">
                {b.gallery!.map((g) => (
                  <img key={g} src={`/api/v1/media/${g}/file`} alt="" className="h-24 w-full cursor-pointer rounded-lg object-cover transition-transform hover:scale-[1.02]" loading="lazy" onClick={() => window.open(`/api/v1/media/${g}/file`, '_blank')} />
                ))}
              </div>
            </section>
          )}

          {enabled('products') && (
            <section>
              <h2 className="mono-label mb-3">Products & services</h2>
              {products.length === 0 ? (
                <Card className="py-10 text-center text-sm text-ink3">Catalog coming soon.</Card>
              ) : (
                <div className="grid gap-4 sm:grid-cols-2">
                  {products.map((p) => (
                    <button
                      key={p.id}
                      onClick={() => setProduct(p)}
                      className="overflow-hidden rounded-xl border border-border text-left transition-shadow hover:shadow-cardHover"
                      style={{ background: 'var(--pv-surface)' }}
                    >
                      {p.cover_image_id ? (
                        <img src={`/api/v1/media/${p.cover_image_id}/file`} alt="" className="h-36 w-full object-cover" loading="lazy" />
                      ) : (
                        <div className="flex h-36 items-center justify-center text-3xl" style={{ background: 'var(--pv-bg)' }}>{p.name.charAt(0)}</div>
                      )}
                      <div className="p-4">
                        <div className="flex items-center justify-between gap-2">
                          <p className="truncate text-sm font-semibold">{p.name}</p>
                          {p.badge !== 'none' && <Badge tone="attention">{p.badge}</Badge>}
                        </div>
                        {p.description && <p className="mt-1 line-clamp-2 text-xs" style={{ color: 'var(--pv-muted)' }}>{p.description}</p>}
                        <div className="mt-2 flex items-center justify-between">
                          <span className="font-mono text-sm">{p.call_for_price ? 'Call for price' : priceLabel(p)}</span>
                          {p.variants && p.variants.length > 0 && (
                            <span className="text-xs" style={{ color: 'var(--pv-muted)' }}>{p.variants.length} options</span>
                          )}
                        </div>
                      </div>
                    </button>
                  ))}
                </div>
              )}
            </section>
          )}

          {product && (
            <ProductModal
              product={product}
              businessId={b.id}
              isOwner={data?.is_owner ?? false}
              onClose={() => setProduct(null)}
            />
          )}

          <section className="border-t border-border pt-6">
            <ReviewsSection businessId={b.id} isOwner={data?.is_owner ?? false} />
          </section>

          <section className="border-t border-border pt-6">
            <QASection businessId={b.id} />
          </section>

          <section className="border-t border-border pt-6">
            <CommentsSection businessId={b.id} />
          </section>

          <section className="border-t border-border pt-6">
            <UpdatesSection businessId={b.id} isOwner={data?.is_owner ?? false} />
          </section>
        </div>

        <aside className="space-y-6">
          {enabled('hours') && (
            <Card className="space-y-4">
              <h2 className="mono-label">Hours</h2>
              <ul className="space-y-1.5 text-sm">
                {openDays.length === 0 && <li className="text-ink3">No hours set</li>}
                {openDays.map(([d, h]) => (
                  <li key={d} className="flex items-center justify-between">
                    <span className="text-ink2">{capitalize(d)}</span>
                    <span className="font-mono text-ink">{h?.open}–{h?.close}</span>
                  </li>
                ))}
              </ul>
              <p className="flex items-center gap-1.5 border-t border-border pt-3 text-xs text-ink3">
                <Clock className="h-3 w-3" /> Local time
              </p>
            </Card>
          )}

          {enabled('contact') && (
            <Card className="space-y-3">
              <h2 className="mono-label">Location</h2>
              <p className="flex items-start gap-2 text-sm text-ink2"><MapPin className="mt-0.5 h-4 w-4 shrink-0" /> {b.address}, {b.city}, {b.country}</p>
              <div className="h-36 overflow-hidden rounded-lg border border-border">
                <MiniMapLive lat={b.lat} lng={b.lng} name={b.name} />
              </div>
              <a href={`https://www.openstreetmap.org/?mlat=${b.lat}&mlon=${b.lng}#map=16/${b.lat}/${b.lng}`} target="_blank" rel="noreferrer" className="text-sm text-ink underline underline-offset-4 hover:text-ink2">
                Open in map
              </a>
            </Card>
          )}

          <Card className="space-y-3">
            <h2 className="mono-label">Contact</h2>
            {b.contact.phone && <ContactRow icon={<Phone className="h-4 w-4" />} label={b.contact.phone} href={`tel:${b.contact.phone}`} />}
            {b.contact.email && <ContactRow icon={<Mail className="h-4 w-4" />} label={b.contact.email} href={`mailto:${b.contact.email}`} />}
            {b.contact.website && <ContactRow icon={<Globe className="h-4 w-4" />} label={b.contact.website} href={b.contact.website} />}
            {Object.entries(SOCIALS)
              .filter(([k]) => b.contact[k])
              .map(([k, label]) => {
                const handle = b.contact[k]
                const urls: Record<string, string> = {
                  whatsapp: `https://wa.me/${handle}`,
                  instagram: `https://instagram.com/${handle}`,
                  tiktok: `https://tiktok.com/@${handle}`,
                  facebook: `https://facebook.com/${handle}`,
                  x: `https://x.com/${handle}`,
                  youtube: `https://youtube.com/@${handle}`,
                  line: `https://line.me/R/ti/p/@${handle}`,
                  telegram: `https://t.me/${handle}`,
                }
                return (
                  <ContactRow
                    key={k}
                    icon={<span className="text-ink3">↗</span>}
                    label={`${label} · @${handle}`}
                    href={urls[k] ?? handle}
                  />
                )
              })}
          </Card>
        </aside>
      </div>

      {data?.similar.length ? (
        <div className="mx-auto max-w-6xl px-4 pb-14 sm:px-6 lg:px-8">
          <h2 className="mono-label mb-4">Similar businesses</h2>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {data.similar.map((s) => (
              <Link key={s.id} to={`/b/${s.slug}`} className="card flex items-center gap-3 p-4 transition-shadow hover:shadow-cardHover">
                {s.logo_url ? <img src={s.logo_url} alt="" className="h-10 w-10 rounded-lg object-cover" /> : <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-surface2 text-sm font-semibold">{s.name.charAt(0)}</div>}
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-ink">{s.name}</p>
                  <p className="text-xs text-ink3">{s.city}</p>
                </div>
              </Link>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  )
}

function FollowButton({ businessId, loggedIn }: { businessId: string; loggedIn: boolean }) {
  const qc = useQueryClient()
  const [following, setFollowing] = useState(false)
  useQuery({
    queryKey: ['follow', businessId],
    queryFn: () => api<{ following: boolean }>(`/businesses/${businessId}/follow`),
    enabled: loggedIn,
  })
  useEffect(() => {
    if (loggedIn) {
      void api<{ following: boolean }>(`/businesses/${businessId}/follow`).then((r) => setFollowing(r.following)).catch(() => undefined)
    }
  }, [businessId, loggedIn])
  if (!loggedIn) return null
  return (
    <Button
      variant={following ? 'primary' : 'secondary'}
      onClick={async () => {
        setFollowing((v) => !v)
        try {
          await api(`/businesses/${businessId}/follow`, { method: following ? 'DELETE' : 'PUT' })
        } catch {
          setFollowing((v) => !v)
        }
        qc.invalidateQueries({ queryKey: ['follow', businessId] })
      }}
    >
      {following ? 'Following' : 'Follow'}
    </Button>
  )
}

function MessageButton({ businessId, loggedIn, slug }: { businessId: string; loggedIn: boolean; slug: string }) {
  const navigate = useNavigate()
  if (!loggedIn) {
    return (
      <Link to={`/login?next=/b/${slug}`}>
        <Button><MessageSquare className="h-4 w-4" /> Contact</Button>
      </Link>
    )
  }
  return (
    <Button
      onClick={async () => {
        const r = await api<{ thread: { id: string } }>('/threads', { method: 'POST', body: { business_id: businessId } })
        navigate(`/me/messages/${r.thread.id}`)
      }}
    >
      <MessageSquare className="h-4 w-4" /> Message
    </Button>
  )
}

function ContactRow({ icon, label, href }: { icon: React.ReactNode; label: string; href: string }) {
  // Owner-controlled values (website field, social handles) pass through a
  // scheme allowlist — a `javascript:` website previously executed on click.
  const safe = safeExternalUrl(href)
  if (!safe) return null
  return (
    <a href={safe} target="_blank" rel="noreferrer" className="flex items-center gap-2 text-sm text-ink underline-offset-4 hover:underline">
      <span className="text-ink3">{icon}</span> {label}
    </a>
  )
}

function capitalize(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1)
}
