import { Clock, MapPin, Phone, Star, Heart, Bolt, Coffee, Wifi, Leaf, Scissors, Camera, Music, Sparkles, Zap } from 'lucide-react'
import type { BusinessDTO, ProductDTO } from '@/lib/api'
import type { ThemeConfig, LayoutConfig } from './StorefrontBuilderPage'

const ICONS: Record<string, React.ComponentType<{ className?: string; style?: React.CSSProperties }>> = {
  star: Star, heart: Heart, bolt: Bolt, coffee: Coffee, wifi: Wifi,
  leaf: Leaf, scissors: Scissors, camera: Camera, music: Music, sparkles: Sparkles, zap: Zap,
}

/**
 * The universal storefront renderer (PRD §10.4): used by the builder's live
 * preview, the owner's "preview as guest", and the public business page.
 * Theme is applied via scoped CSS variables.
 */
export function StorefrontPreview({
  business: b,
  theme,
  layout,
  products,
}: {
  business: BusinessDTO
  theme: ThemeConfig
  layout: LayoutConfig
  products?: ProductDTO[]
}) {
  const c = theme.colors
  const vars = {
    '--pv-bg': c.bg,
    '--pv-surface': c.surface,
    '--pv-ink': c.ink,
    '--pv-accent': c.accent,
    '--pv-accent-ink': c.bg,
    '--pv-muted': c.muted ?? c.ink + 'aa',
    '--pv-font': FONTS[theme.font],
  } as React.CSSProperties

  const enabled = (key: string) => layout.sections.find((s) => s.key === key)?.enabled ?? true
  const openDays = Object.entries(b.hours).filter(([, h]) => h && !h.closed)

  return (
    <div style={{ ...vars, background: c.bg, color: c.ink, fontFamily: FONTS[theme.font] }} className="min-h-[420px]">
      {/* Hero */}
      {enabled('hero') && (
        <header className="relative overflow-hidden">
          {b.cover_url && (
            <div className="h-28 w-full" style={{ backgroundImage: `url(${b.cover_url})`, backgroundSize: 'cover', backgroundPosition: 'center' }} />
          )}
          <div className="px-6 py-8 text-center">
            {b.logo_url ? (
              <img src={b.logo_url} alt="" className="mx-auto mb-3 h-14 w-14 rounded-2xl object-cover" style={{ boxShadow: '0 4px 16px rgb(0 0 0 / 0.12)' }} />
            ) : (
              <div className="mx-auto mb-3 flex h-14 w-14 items-center justify-center rounded-2xl text-xl font-semibold" style={{ background: c.surface, color: c.ink }}>
                {b.name.charAt(0)}
              </div>
            )}
            <h1 className="text-2xl font-bold tracking-tight">{b.name}</h1>
            {b.tagline && <p className="mt-1 text-sm" style={{ color: c.muted }}>{b.tagline}</p>}
            <div className="mt-3 flex flex-wrap items-center justify-center gap-2 text-xs" style={{ color: c.muted }}>
              <span>{b.category_name}</span>
              <span>·</span>
              <span>{b.city}, {b.country}</span>
              <span>·</span>
              <span className="font-mono">{"$".repeat(b.price_level ?? 0) || '—'}</span>
            </div>
            <button
              className="mt-5 rounded-full px-6 py-2.5 text-sm font-medium"
              style={{ background: c.accent, color: c['--pv-accent-ink' as keyof typeof c] ?? c.bg }}
            >
              Message us
            </button>
          </div>
        </header>
      )}

      {/* About */}
      {enabled('about') && b.description && (
        <section className="px-6 py-6">
          <SectionTitle theme={theme}>About</SectionTitle>
          <p className="text-sm leading-relaxed" style={{ color: c.muted }}>{b.description}</p>
        </section>
      )}

      {/* Highlights */}
      {enabled('highlights') && layout.highlights.length > 0 && (
        <section className="px-6 pb-6">
          <div className="grid gap-3 sm:grid-cols-3">
            {layout.highlights.map((h, i) => {
              const Icon = ICONS[h.icon] ?? Star
              return (
                <div key={i} className="rounded-xl p-4" style={{ background: c.surface }}>
                  <Icon className="h-4 w-4" style={{ color: c.ink }} />
                  <p className="mt-2 text-sm font-semibold">{h.title}</p>
                  <p className="mt-1 text-xs" style={{ color: c.muted }}>{h.text}</p>
                </div>
              )
            })}
          </div>
        </section>
      )}

      {/* Products */}
      {enabled('products') && (products?.length ?? 0) > 0 && (
        <section className="px-6 pb-6">
          <SectionTitle theme={theme}>Products & services</SectionTitle>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {products!.map((p) => (
              <div key={p.id} className="overflow-hidden rounded-xl" style={{ background: c.surface }}>
                {p.cover_image_id ? (
                  <img src={`/api/v1/media/${p.cover_image_id}/file`} alt="" className="h-28 w-full object-cover" />
                ) : (
                  <div className="flex h-28 items-center justify-center text-2xl" style={{ background: c.bg }}>{p.name.charAt(0)}</div>
                )}
                <div className="p-3">
                  <p className="text-sm font-semibold">{p.name}</p>
                  <p className="mt-0.5 text-xs" style={{ color: c.muted }}>
                    {p.call_for_price ? 'Call for price' : priceLabel(p)}
                  </p>
                </div>
              </div>
            ))}
          </div>
        </section>
      )}

      {/* Hours */}
      {enabled('hours') && openDays.length > 0 && (
        <section className="px-6 pb-6">
          <SectionTitle theme={theme} icon={<Clock className="h-3.5 w-3.5" />}>Hours</SectionTitle>
          <div className="rounded-xl px-4 py-3 text-xs" style={{ background: c.surface }}>
            {openDays.map(([d, h]) => (
              <div key={d} className="flex justify-between py-0.5">
                <span style={{ color: c.muted }}>{cap(d)}</span>
                <span className="font-mono">{h?.open}–{h?.close}</span>
              </div>
            ))}
          </div>
        </section>
      )}

      {/* Contact */}
      {enabled('contact') && (
        <section className="px-6 pb-8">
          <SectionTitle theme={theme} icon={<Phone className="h-3.5 w-3.5" />}>Contact</SectionTitle>
          <div className="space-y-1.5 text-xs" style={{ color: c.muted }}>
            {b.contact.phone && <p className="flex items-center gap-2"><Phone className="h-3 w-3" /> {b.contact.phone}</p>}
            {b.contact.email && <p className="flex items-center gap-2"><MailIcon /> {b.contact.email}</p>}
            <p className="flex items-center gap-2"><MapPin className="h-3 w-3" /> {b.address}, {b.city}</p>
          </div>
        </section>
      )}
    </div>
  )
}

function SectionTitle({ theme, icon, children }: { theme: ThemeConfig; icon?: React.ReactNode; children: React.ReactNode }) {
  return (
    <h2 className="mb-3 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.18em]" style={{ color: theme.colors.muted ?? theme.colors.ink }}>
      {icon} {children}
    </h2>
  )
}

function MailIcon() {
  return (
    <svg viewBox="0 0 24 24" className="h-3 w-3" fill="none" stroke="currentColor" strokeWidth="2">
      <rect x="3" y="5" width="18" height="14" rx="2" />
      <path d="m3 7 9 6 9-6" />
    </svg>
  )
}

// Currency-aware formatting (PRD D5): display prices converted to the viewer's currency.
import { formatMoney, useCurrency } from '@/stores/currency'

export function priceLabel(p: ProductDTO, rates?: Record<string, number>, display?: string): string {
  const st = useCurrency.getState()
  const r = rates ?? st.rates
  const d = display ?? st.display
  if (p.variants && p.variants.length > 0) {
    const prices = p.variants.filter((v) => v.price !== null).map((v) => v.price as number)
    if (prices.length > 0) {
      const min = Math.min(...prices)
      const max = Math.max(...prices)
      const f = (n: number) => formatMoney(n, p.currency, d, r)
      return min === max ? f(min) : `${f(min)}–${f(max)}`
    }
  }
  if (p.base_price !== null && p.base_price !== undefined) return formatMoney(p.base_price, p.currency, d, r)
  return '—'
}

const FONTS: Record<string, string> = {
  inter: "'Inter', system-ui, sans-serif",
  serif: 'Georgia, "Times New Roman", serif',
  mono: 'ui-monospace, SFMono-Regular, Menlo, monospace',
}

function cap(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1)
}
