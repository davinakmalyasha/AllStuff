import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, CheckCircle2, Eye, Loader2, Monitor, Smartphone } from 'lucide-react'
import { api, type BusinessDTO } from '@/lib/api'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner } from '@/components/ui/Spinner'
import { StorefrontPreview } from './StorefrontPreview'

const PRESETS: Record<string, { name: string; colors: Record<string, string>; font: string }> = {
  paper: { name: 'Paper', colors: { bg: '#ffffff', surface: '#fafafa', ink: '#18181b', accent: '#18181b', muted: '#71717a' }, font: 'inter' },
  ink: { name: 'Ink', colors: { bg: '#0a0a0a', surface: '#141416', ink: '#f4f4f5', accent: '#f4f4f5', muted: '#a1a1aa' }, font: 'inter' },
  bone: { name: 'Bone', colors: { bg: '#f6f2ea', surface: '#fdfbf7', ink: '#2b2620', accent: '#2b2620', muted: '#8a8178' }, font: 'serif' },
  slate: { name: 'Slate', colors: { bg: '#f1f5f9', surface: '#ffffff', ink: '#0f172a', accent: '#0f172a', muted: '#64748b' }, font: 'mono' },
  forest: { name: 'Forest', colors: { bg: '#f0f7f2', surface: '#ffffff', ink: '#14301f', accent: '#1e6b3c', muted: '#5c7363' }, font: 'inter' },
  sunset: { name: 'Sunset', colors: { bg: '#fdf1ec', surface: '#fffbf9', ink: '#3a1c12', accent: '#c2410c', muted: '#8d6a5d' }, font: 'serif' },
  ocean: { name: 'Ocean', colors: { bg: '#eef4fb', surface: '#ffffff', ink: '#0c2a44', accent: '#0369a1', muted: '#55738c' }, font: 'inter' },
  mono_dark: { name: 'Mono Dark', colors: { bg: '#101014', surface: '#1a1a21', ink: '#e8e8ec', accent: '#7dd3fc', muted: '#8b8b98' }, font: 'mono' },
}

const SECTION_LABELS: Record<string, string> = {
  hero: 'Hero',
  about: 'About',
  highlights: 'Highlights',
  products: 'Products',
  gallery: 'Gallery',
  hours: 'Hours',
  contact: 'Contact',
}

export type ThemeConfig = { template: string; colors: Record<string, string>; font: string }
export type SectionConfig = { key: string; enabled: boolean }
export type LayoutConfig = {
  sections: SectionConfig[]
  highlights: { icon: string; title: string; text: string }[]
}

const defaultLayout = (): LayoutConfig => ({
  sections: [
    { key: 'hero', enabled: true },
    { key: 'about', enabled: true },
    { key: 'highlights', enabled: true },
    { key: 'products', enabled: true },
    { key: 'gallery', enabled: false },
    { key: 'hours', enabled: true },
    { key: 'contact', enabled: true },
  ],
  highlights: [
    { icon: 'star', title: 'Quality first', text: 'Every detail matters.' },
    { icon: 'heart', title: 'Loved locally', text: 'Backed by the neighborhood.' },
    { icon: 'bolt', title: 'Fast & easy', text: 'No hassle, ever.' },
  ],
})

export function StorefrontBuilderPage() {
  const qc = useQueryClient()
  const business = useActiveBusiness()
  const [device, setDevice] = useState<'desktop' | 'mobile'>('desktop')
  const [saving, setSaving] = useState(false)
  const [flash, setFlash] = useState('')

  const { data } = useQuery({
    queryKey: ['business', business?.id],
    queryFn: () => api<{ business: BusinessDTO }>(`/businesses/${business!.id}`),
    enabled: !!business,
  })
  const b = data?.business

  const [theme, setTheme] = useState<ThemeConfig>({ template: 'paper', colors: PRESETS.paper.colors, font: 'inter' })
  const [layout, setLayout] = useState<LayoutConfig>(defaultLayout())
  const [initialized, setInitialized] = useState(false)

  // Hydrate once from the business draft theme/layout.
  if (b && !initialized) {
    const t = (b as unknown as { theme?: Partial<ThemeConfig> }).theme
    if (t?.colors) {
      setTheme({ template: t.template ?? 'paper', colors: { ...PRESETS.paper.colors, ...t.colors }, font: t.font ?? 'inter' })
    }
    const l = (b as unknown as { layout?: Partial<LayoutConfig> }).layout
    if (l?.sections) {
      setLayout({ sections: l.sections, highlights: l.highlights ?? defaultLayout().highlights })
    }
    setInitialized(true)
  }

  const save = async () => {
    if (!b) return
    setSaving(true)
    setFlash('')
    try {
      await api(`/businesses/${b.id}/storefront`, { method: 'PUT', body: { theme, layout } })
      setFlash('Draft saved')
      setTimeout(() => setFlash(''), 2000)
    } finally {
      setSaving(false)
    }
  }

  const publishMutation = useMutation({
    mutationFn: () => api(`/businesses/${b!.id}/publish`, { method: 'POST' }),
    onSuccess: () => {
      setFlash('Published — your storefront is live')
      qc.invalidateQueries({ queryKey: ['business', b?.id] })
      setTimeout(() => setFlash(''), 2500)
    },
  })

  const unpublishMutation = useMutation({
    mutationFn: () => api(`/businesses/${b!.id}/unpublish`, { method: 'POST' }),
    onSuccess: () => {
      setFlash('Unpublished')
      qc.invalidateQueries({ queryKey: ['business', b?.id] })
      setTimeout(() => setFlash(''), 2000)
    },
  })

  if (!business) {
    return (
      <div className="mx-auto max-w-xl">
        <h1 className="text-2xl font-semibold tracking-tight">No business selected</h1>
        <p className="mt-2 text-sm text-ink2">Register a business first, then design its storefront.</p>
      </div>
    )
  }
  if (!b) return <PageSpinner />

  const setColor = (key: string, value: string) =>
    setTheme((t) => ({ ...t, colors: { ...t.colors, [key]: value } }))

  const toggleSection = (key: string) =>
    setLayout((l) => ({
      ...l,
      sections: l.sections.map((s) => (s.key === key ? { ...s, enabled: !s.enabled } : s)),
    }))

  const moveSection = (key: string, dir: -1 | 1) =>
    setLayout((l) => {
      const idx = l.sections.findIndex((s) => s.key === key)
      const j = idx + dir
      if (idx < 0 || j < 0 || j >= l.sections.length) return l
      const sections = [...l.sections]
      ;[sections[idx], sections[j]] = [sections[j], sections[idx]]
      return { ...l, sections }
    })

  const setHighlight = (i: number, field: 'icon' | 'title' | 'text', value: string) =>
    setLayout((l) => {
      const highlights = l.highlights.map((h, idx) => (idx === i ? { ...h, [field]: value } : h))
      return { ...l, highlights }
    })

  return (
    <div className="mx-auto max-w-6xl">
      <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="mono-label mb-1">Storefront builder</p>
          <h1 className="text-2xl font-semibold tracking-tight">{b.name}</h1>
          <div className="mt-1 flex items-center gap-2 text-xs text-ink3">
            {b.last_published_at ? (
              <span>Published {new Date(b.last_published_at).toLocaleString()}</span>
            ) : (
              <span>Never published</span>
            )}
            {flash && <Badge tone="positive" dot>{flash}</Badge>}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <a href={`/b/${b.slug}?draft=1`} target="_blank" rel="noreferrer">
            <Button variant="secondary"><Eye className="h-4 w-4" /> Preview as guest</Button>
          </a>
          <a href={`/b/${b.slug}`} target="_blank" rel="noreferrer">
            <Button variant="secondary"><Eye className="h-4 w-4" /> View live</Button>
          </a>
          <Button variant="secondary" onClick={() => void save()} disabled={saving}>
            {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Save draft'}
          </Button>
          {b.last_published_at ? (
            <Button variant="secondary" onClick={() => void unpublishMutation.mutateAsync()} disabled={unpublishMutation.isPending}>
              Unpublish
            </Button>
          ) : (
            <Button onClick={() => void publishMutation.mutateAsync()} disabled={publishMutation.isPending}>
              <CheckCircle2 className="h-4 w-4" /> Publish
            </Button>
          )}
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-[360px_1fr]">
        {/* Controls */}
        <div className="space-y-5">
          <Card className="space-y-4">
            <p className="mono-label">Theme</p>
            <div className="grid grid-cols-2 gap-2">
              {Object.entries(PRESETS).map(([key, p]) => (
                <button
                  key={key}
                  onClick={() => setTheme({ template: key, colors: p.colors, font: p.font })}
                  className={`flex items-center gap-2 rounded-lg border p-2.5 text-left text-xs transition-colors ${
                    theme.template === key ? 'border-ink bg-surface2' : 'border-border hover:bg-surface2'
                  }`}
                >
                  <span className="flex h-6 w-6 shrink-0 rounded" style={{ background: p.colors.bg, border: `1px solid ${p.colors.ink}33` }} />
                  <span className="text-ink">{p.name}</span>
                </button>
              ))}
            </div>
            <div className="space-y-2">
              <label className="block text-xs text-ink2">
                Background
                <input type="color" value={theme.colors.bg} onChange={(e) => setColor('bg', e.target.value)} className="mt-1 h-8 w-full rounded border border-border bg-surface" />
              </label>
              <label className="block text-xs text-ink2">
                Ink (text)
                <input type="color" value={theme.colors.ink} onChange={(e) => setColor('ink', e.target.value)} className="mt-1 h-8 w-full rounded border border-border bg-surface" />
              </label>
              <label className="block text-xs text-ink2">
                Accent (buttons)
                <input type="color" value={theme.colors.accent} onChange={(e) => setColor('accent', e.target.value)} className="mt-1 h-8 w-full rounded border border-border bg-surface" />
              </label>
              <label className="block text-xs text-ink2">
                Font
                <select
                  value={theme.font}
                  onChange={(e) => setTheme((t) => ({ ...t, font: e.target.value }))}
                  className="mt-1 h-8 w-full rounded border border-border bg-surface px-2 text-sm text-ink"
                >
                  <option value="inter">Inter</option>
                  <option value="serif">Serif</option>
                  <option value="mono">Mono</option>
                </select>
              </label>
            </div>
          </Card>

          <Card className="space-y-2">
            <p className="mono-label">Sections</p>
            {layout.sections.map((s, i) => (
              <div key={s.key} className="flex items-center gap-2">
                <label className={`flex flex-1 items-center gap-2 rounded-lg border px-3 py-2 text-sm ${s.enabled ? 'border-border text-ink' : 'border-dashed border-border text-ink3'}`}>
                  <input type="checkbox" checked={s.enabled} onChange={() => toggleSection(s.key)} className="h-3.5 w-3.5 accent-black dark:accent-white" />
                  {SECTION_LABELS[s.key] ?? s.key}
                </label>
                <button onClick={() => moveSection(s.key, -1)} disabled={i === 0} className="rounded p-1 text-ink3 hover:text-ink disabled:opacity-30" aria-label="Move up">
                  <ArrowUp className="h-3.5 w-3.5" />
                </button>
                <button onClick={() => moveSection(s.key, 1)} disabled={i === layout.sections.length - 1} className="rounded p-1 text-ink3 hover:text-ink disabled:opacity-30" aria-label="Move down">
                  <ArrowDown className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
          </Card>

          <Card className="space-y-3">
            <p className="mono-label">Highlights (About card)</p>
            {layout.highlights.map((h, i) => (
              <div key={i} className="space-y-1.5 rounded-lg border border-border p-2.5">
                <input value={h.title} onChange={(e) => setHighlight(i, 'title', e.target.value)} placeholder="Title" className="h-8 w-full rounded border border-border bg-surface px-2 text-sm text-ink" />
                <input value={h.text} onChange={(e) => setHighlight(i, 'text', e.target.value)} placeholder="Text" className="h-8 w-full rounded border border-border bg-surface px-2 text-sm text-ink" />
              </div>
            ))}
          </Card>
        </div>

        {/* Live preview */}
        <div>
          <div className="mb-3 flex items-center justify-between">
            <p className="mono-label">Live preview</p>
            <div className="flex rounded-lg border border-border p-0.5">
              <button
                onClick={() => setDevice('desktop')}
                className={`flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs ${device === 'desktop' ? 'bg-accent text-accent-ink' : 'text-ink2'}`}
              >
                <Monitor className="h-3.5 w-3.5" /> Desktop
              </button>
              <button
                onClick={() => setDevice('mobile')}
                className={`flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs ${device === 'mobile' ? 'bg-accent text-accent-ink' : 'text-ink2'}`}
              >
                <Smartphone className="h-3.5 w-3.5" /> Mobile
              </button>
            </div>
          </div>
          <div className="overflow-hidden rounded-xl border border-border bg-surface shadow-card">
            <div className={device === 'mobile' ? 'mx-auto my-4 max-w-[375px]' : ''}>
              <StorefrontPreview business={b} theme={theme} layout={layout} />
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
