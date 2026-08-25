import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Loader2, Pause, Play, XCircle } from 'lucide-react'
import { api, uploadMedia, type BusinessDTO, type CategoryDTO, type MediaDTO } from '@/lib/api'
import { TIMEZONES } from '@/lib/timezones'
import { TeamSection } from './TeamSection'
import { AmenityEditor, SpecialHoursEditor, type SpecialHours } from './AmenityEditors'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { Input } from '@/components/ui/Input'
import { PageSpinner } from '@/components/ui/Spinner'

const DAYS = [
  ['mon', 'Monday'], ['tue', 'Tuesday'], ['wed', 'Wednesday'], ['thu', 'Thursday'],
  ['fri', 'Friday'], ['sat', 'Saturday'], ['sun', 'Sunday'],
] as const

const SOCIALS = ['instagram', 'tiktok', 'facebook', 'x', 'youtube', 'line', 'telegram'] as const

export function SettingsPage() {
  const qc = useQueryClient()
  const business = useActiveBusiness()

  const { data } = useQuery({
    queryKey: ['business', business?.id],
    queryFn: () => api<{ business: BusinessDTO }>(`/businesses/${business!.id}`),
    enabled: !!business,
  })
  const b = data?.business

  const { data: catData } = useQuery({
    queryKey: ['categories'],
    queryFn: () => api<{ categories: CategoryDTO[] }>('/categories'),
  })
  const leafCats: CategoryDTO[] = []
  const walk = (cs: CategoryDTO[]) => {
    for (const c of cs) {
      if (c.children?.length) walk(c.children)
      else leafCats.push(c)
    }
  }
  walk(catData?.categories ?? [])

  const [form, setForm] = useState<Record<string, unknown>>({})
  const [hours, setHours] = useState<Record<string, { open: string; close: string; closed: boolean }>>({})
  const [saving, setSaving] = useState(false)
  const [flash, setFlash] = useState('')
  const [error, setError] = useState('')
  const [logo, setLogo] = useState<MediaDTO | null>(null)
  const [cover, setCover] = useState<MediaDTO | null>(null)

  // Hydrate in an effect KEYED ON THE BUSINESS ID: the old render-phase
  // `if (b && !hydrated)` never reset when the active business changed, so
  // switching businesses showed — and saving wrote — the PREVIOUS
  // business's values into the new one.
  useEffect(() => {
    if (!b) return
    setForm({
      name: b.name, tagline: b.tagline ?? '', description: b.description,
      category_id: b.category_id, address: b.address, city: b.city, country: b.country,
      lat: String(b.lat), lng: String(b.lng), timezone: b.timezone || 'UTC',
      amenities: b.amenities ?? [],
      special_hours: (b.special_hours as Record<string, { open: string; close: string; closed: boolean }> | undefined) ?? {},
      price_level: b.price_level ? String(b.price_level) : '',
      currency: b.currency, tags: b.tags.join(', '),
      founded_year: b.founded_year ? String(b.founded_year) : '',
      ...Object.fromEntries(Object.entries(b.contact ?? {}).filter(([, v]) => typeof v === 'string')),
    })
    const h: Record<string, { open: string; close: string; closed: boolean }> = {}
    for (const [day, val] of Object.entries(b.hours ?? {})) {
      if (val && typeof val === 'object') {
        const d = val as { open?: string; close?: string; closed?: boolean }
        h[day] = { open: d.open ?? '09:00', close: d.close ?? '17:00', closed: !!d.closed }
      }
    }
    setHours(h)
    setLogo(null)
    setCover(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [b?.id, b?.updated_at])

  const str = (v: unknown) => (typeof v === 'string' ? v : '')

  const set = (key: string) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [key]: e.target.value }))

  const save = async () => {
    if (!b) return
    setSaving(true)
    setError('')
    try {
      const payload: Record<string, unknown> = {
        name: form.name, tagline: form.tagline, description: form.description,
        category_id: form.category_id, address: form.address, city: form.city, country: form.country,
        lat: form.lat ? Number(form.lat) : 0, lng: form.lng ? Number(form.lng) : 0,
        // The timezone select rendered but was never sent — owners "saved" it
        // and watched it revert, leaving open-now computed in UTC.
        timezone: str(form.timezone) || 'UTC',
        price_level: form.price_level ? Number(form.price_level) : null,
        currency: form.currency, tags: String(form.tags ?? '').split(',').map((t) => t.trim()).filter(Boolean),
        founded_year: form.founded_year ? Number(form.founded_year) : null,
        contact: Object.fromEntries(
          ['phone', 'email', 'website', 'whatsapp', ...SOCIALS].map((k) => [k, form[k] ?? '']),
        ),
        hours,
      }
      if (logo) payload.logo_url = logo.url
      if (cover) payload.cover_url = cover.url
      if (form.amenities) payload.amenities = form.amenities
      if (form.special_hours) payload.special_hours = form.special_hours
      await api(`/businesses/${b.id}`, { method: 'PATCH', body: payload })
      setFlash('Settings saved')
      setTimeout(() => setFlash(''), 2000)
      qc.invalidateQueries({ queryKey: ['business', b.id] })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const action = useMutation({
    mutationFn: (verb: 'pause' | 'reopen' | 'close') => api(`/businesses/${b!.id}/${verb}`, { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['business', b?.id] }),
  })

  const uploadLogo = async (file: File, kind: 'logo' | 'cover') => {
    const r = await uploadMedia(kind, file)
    if (kind === 'logo') setLogo(r.media)
    else setCover(r.media)
  }

  if (!business) {
    return <div className="mx-auto max-w-xl"><h1 className="text-2xl font-semibold tracking-tight">No business selected</h1></div>
  }
  if (!b) return <PageSpinner />

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <div>
        <p className="mono-label mb-1">Settings</p>
        <h1 className="text-2xl font-semibold tracking-tight">{b.name}</h1>
        <div className="mt-1 flex items-center gap-2">
          <Badge tone={b.status === 'verified' ? 'positive' : b.status === 'paused' ? 'neutral' : 'attention'} dot>{b.status}</Badge>
          {flash && <span className="text-xs text-ink2">{flash}</span>}
        </div>
      </div>

      {error && <p className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">{error}</p>}

      <Card className="space-y-4">
        <p className="mono-label">Basics</p>
        <div className="grid gap-4 sm:grid-cols-2">
          <Input label="Name" value={String(form.name ?? '')} onChange={set('name')} />
          <Input label="Tagline" value={String(form.tagline ?? '')} onChange={set('tagline')} />
        </div>
        <div>
          <label className="mb-1.5 block text-sm font-medium text-ink">Category (leaf)</label>
          <select value={str(form.category_id ?? '')} onChange={(e) => setForm((f) => ({ ...f, category_id: e.target.value }))} className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink">
            {leafCats.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
          <p className="mt-1 text-xs text-ink3">Changing category triggers re-verification (PRD §8.2).</p>
        </div>
        <textarea rows={4} value={str(form.description ?? '')} onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))} placeholder="Description (min 50 chars)" className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink" />
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <Input label="Price level" type="number" min={1} max={4} value={str(form.price_level ?? '')} onChange={set('price_level')} />
          <Input label="Currency" value={str(form.currency ?? '')} onChange={set('currency')} maxLength={3} />
          <Input label="Founded" type="number" value={str(form.founded_year ?? '')} onChange={set('founded_year')} />
          <Input label="Tags" value={str(form.tags ?? '')} onChange={set('tags')} />
        </div>
        <div className="grid grid-cols-2 gap-4">
          <label className="flex cursor-pointer items-center gap-2 rounded-lg border border-dashed border-border p-3 text-sm text-ink2 hover:bg-surface2">
            {logo ? 'Logo: uploaded ✓' : 'Upload logo'} <input type="file" accept="image/png,image/jpeg,image/webp" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) void uploadLogo(f, 'logo') }} />
          </label>
          <label className="flex cursor-pointer items-center gap-2 rounded-lg border border-dashed border-border p-3 text-sm text-ink2 hover:bg-surface2">
            {cover ? 'Cover: uploaded ✓' : 'Upload cover'} <input type="file" accept="image/png,image/jpeg,image/webp" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) void uploadLogo(f, 'cover') }} />
          </label>
        </div>
      </Card>

      <Card className="space-y-4">
        <p className="mono-label">Location</p>
        <Input label="Address" value={str(form.address ?? '')} onChange={set('address')} />
        <div className="grid grid-cols-2 gap-4">
          <Input label="City" value={str(form.city ?? '')} onChange={set('city')} />
          <Input label="Country" value={str(form.country ?? '')} onChange={set('country')} />
        </div>
        <div className="grid grid-cols-2 gap-4">
          <Input label="Latitude" value={str(form.lat ?? '')} onChange={set('lat')} />
          <Input label="Longitude" value={str(form.lng ?? '')} onChange={set('lng')} />
        </div>
        <div>
          <label className="mb-1.5 block text-sm font-medium text-ink">Timezone</label>
          <select value={str(form.timezone ?? 'UTC')} onChange={(e) => setForm((f) => ({ ...f, timezone: e.target.value }))} className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink">
            {TIMEZONES.map((tz) => <option key={tz} value={tz}>{tz}</option>)}
          </select>
        </div>
      </Card>

      <Card className="space-y-4">
        <p className="mono-label">Contact & socials</p>
        <div className="grid gap-4 sm:grid-cols-2">
          <Input label="Phone" value={str(form.phone ?? '')} onChange={set('phone')} />
          <Input label="Email" value={str(form.email ?? '')} onChange={set('email')} />
          <Input label="Website" value={str(form.website ?? '')} onChange={set('website')} />
          <Input label="WhatsApp" value={str(form.whatsapp ?? '')} onChange={set('whatsapp')} />
          {SOCIALS.map((s) => (
            <Input key={s} label={s} value={str(form[s])} onChange={set(s)} />
          ))}
        </div>
      </Card>

      <Card className="space-y-4">
        <p className="mono-label">Amenities & special hours</p>
<AmenityEditor value={(form.amenities ?? []) as string[]} onChange={(a) => setForm((f) => ({ ...f, amenities: a }))} />
<SpecialHoursEditor value={(form.special_hours ?? {}) as SpecialHours} onChange={(s) => setForm((f) => ({ ...f, special_hours: s }))} />
      </Card>

      <Card className="space-y-3">
        <p className="mono-label">Weekly hours</p>
        {DAYS.map(([key, label]) => {
          const d = hours[key] ?? { open: '09:00', close: '17:00', closed: false }
          return (
            <div key={key} className="flex items-center gap-3">
              <label className="flex w-28 shrink-0 items-center gap-2 text-sm text-ink">
                <input type="checkbox" checked={!d.closed} onChange={(e) => setHours((h) => ({ ...h, [key]: { ...d, closed: !e.target.checked } }))} className="h-4 w-4 accent-black dark:accent-white" />
                {label}
              </label>
              {!d.closed && (
                <div className="flex items-center gap-2">
                  <input type="time" value={d.open} onChange={(e) => setHours((h) => ({ ...h, [key]: { ...d, open: e.target.value } }))} className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink" />
                  <span className="text-ink3">–</span>
                  <input type="time" value={d.close} onChange={(e) => setHours((h) => ({ ...h, [key]: { ...d, close: e.target.value } }))} className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink" />
                </div>
              )}
            </div>
          )
        })}
      </Card>

      <div className="flex justify-end">
        <Button onClick={() => void save()} disabled={saving}>
          {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Save settings'}
        </Button>
      </div>

      <TeamSection />

      <Card className="space-y-3 border-red-300 dark:border-red-900">
        <p className="mono-label flex items-center gap-2 text-red-700 dark:text-red-400"><AlertTriangle className="h-3.5 w-3.5" /> Danger zone</p>
        <div className="flex flex-wrap gap-2">
          {b.status === 'paused' ? (
            <Button variant="secondary" onClick={() => void action.mutateAsync('reopen')} disabled={action.isPending}>
              <Play className="h-4 w-4" /> Reopen
            </Button>
          ) : (
            <Button variant="secondary" onClick={() => void action.mutateAsync('pause')} disabled={action.isPending || b.status !== 'verified'}>
              <Pause className="h-4 w-4" /> Pause (hide from search)
            </Button>
          )}
          <Button variant="danger" onClick={() => { if (window.confirm(`Permanently close "${b.name}"? This cannot be undone.`)) void action.mutateAsync('close') }} disabled={action.isPending || b.status === 'closed'}>
            <XCircle className="h-4 w-4" /> Close permanently
          </Button>
        </div>
        <p className="text-xs text-ink3">Paused: hidden from search/map, page shows "temporarily closed". Closed: 410, irreversible (PRD §8.2).</p>
      </Card>
    </div>
  )
}
