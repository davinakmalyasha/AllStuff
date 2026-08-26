import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  CloudUpload,
  Loader2,
  MapPin,
  Trash2,
} from 'lucide-react'
import { api, uploadMedia, DOC_KINDS, type BusinessDTO, type CategoryDTO, type VerificationDocumentDTO, type MediaDTO } from '@/lib/api'
import { TIMEZONES } from '@/lib/timezones'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Card } from '@/components/ui/Card'
import { PageSpinner } from '@/components/ui/Spinner'
import { ApiError } from '@/lib/api'
import { resetFileInput } from '@/lib/format'

const STEPS = ['Info', 'Location', 'Contact', 'Hours', 'Documents', 'Review']

const DAYS = [
  ['mon', 'Monday'],
  ['tue', 'Tuesday'],
  ['wed', 'Wednesday'],
  ['thu', 'Thursday'],
  ['fri', 'Friday'],
  ['sat', 'Saturday'],
  ['sun', 'Sunday'],
] as const

const SOCIAL_FIELDS = [
  ['instagram', 'Instagram'],
  ['tiktok', 'TikTok'],
  ['facebook', 'Facebook'],
  ['x', 'X (Twitter)'],
  ['youtube', 'YouTube'],
  ['line', 'Line'],
  ['telegram', 'Telegram'],
] as const

/** Upload state only reads `url` (and truthiness); resumed drafts carry just
 * a URL, so a minimal record is enough to keep the review step honest. */
function mediaFromUrl(url: string | null | undefined): MediaDTO | null {
  if (!url) return null
  return { id: '', kind: '', original_name: '', mime: '', size: 0, width: null, height: null, url, thumb_url: null, created_at: '' }
}

interface WizardState {
  name: string
  tagline: string
  description: string
  category_id: string
  address: string
  city: string
  country: string
  lat: string
  lng: string
  phone: string
  email: string
  website: string
  whatsapp: string
  [k: string]: string
}

export function BusinessWizardPage() {
  const navigate = useNavigate()
  const [business, setBusiness] = useState<BusinessDTO | null>(null)
  const [step, setStep] = useState(0)
  const [form, setForm] = useState<WizardState>({
    name: '', tagline: '', description: '', category_id: '',
    address: '', city: '', country: '', lat: '', lng: '',
    timezone: 'UTC',
    phone: '', email: '', website: '', whatsapp: '',
    instagram: '', tiktok: '', facebook: '', x: '', youtube: '', line: '', telegram: '',
  })
  const [hours, setHours] = useState<Record<string, { open: string; close: string; closed: boolean }>>({
    mon: { open: '09:00', close: '17:00', closed: false },
    tue: { open: '09:00', close: '17:00', closed: false },
    wed: { open: '09:00', close: '17:00', closed: false },
    thu: { open: '09:00', close: '17:00', closed: false },
    fri: { open: '09:00', close: '17:00', closed: false },
    sat: { open: '10:00', close: '14:00', closed: false },
    sun: { open: '10:00', close: '14:00', closed: true },
  })
  const [logo, setLogo] = useState<MediaDTO | null>(null)
  const [cover, setCover] = useState<MediaDTO | null>(null)
  const [docs, setDocs] = useState<VerificationDocumentDTO[]>([])
  const [uploading, setUploading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [savedFlash, setSavedFlash] = useState(false)

  const { data: catData } = useQuery({
    queryKey: ['categories'],
    queryFn: () => api<{ categories: CategoryDTO[] }>('/categories'),
  })

  const leafCategories = useMemo(() => {
    const out: CategoryDTO[] = []
    const walk = (cats: CategoryDTO[]) => {
      for (const c of cats) {
        if (c.children?.length) walk(c.children)
        else out.push(c)
      }
    }
    walk(catData?.categories ?? [])
    return out
  }, [catData])

  // Resume the latest editable draft (PRD §5.4.1: auto-saved, resumable);
  // create a fresh one only if nothing exists. StrictMode re-runs effects on
  // the same instance, so the ref guard prevents double-creation.
  const startedRef = useRef(false)
  useEffect(() => {
    if (startedRef.current) return
    startedRef.current = true

    const hydrate = (b: BusinessDTO) => {
      setBusiness(b)
      setForm((f) => {
        const next = { ...f }
        for (const [k, v] of Object.entries(b.contact ?? {})) {
          if (typeof v === 'string') next[k] = v
        }
        next.name = b.name === 'Untitled business' ? '' : b.name
        next.tagline = b.tagline ?? ''
        next.description = b.description
        next.category_id = b.category_id
        next.address = b.address
        next.city = b.city
        next.country = b.country
        next.lat = b.lat ? String(b.lat) : ''
        next.lng = b.lng ? String(b.lng) : ''
        // Carry the draft's timezone through resume — without this the
        // Location step silently flipped an Asia/Jakarta business back to
        // UTC on save.
        next.timezone = b.timezone || next.timezone || 'UTC'
        return next
      })
      // Seed upload state from the saved draft so a resumed draft doesn't
      // show "Missing — required" for its existing logo in the review step.
      setLogo(mediaFromUrl(b.logo_url))
      setCover(mediaFromUrl(b.cover_url))
      setHours((prev) => {
        const next = { ...prev }
        for (const [day, val] of Object.entries(b.hours ?? {})) {
          if (val && typeof val === 'object') {
            const d = val as { open?: string; close?: string; closed?: boolean }
            next[day] = { open: d.open ?? '09:00', close: d.close ?? '17:00', closed: !!d.closed }
          }
        }
        return next
      })
      setStep(0)
    }

    const init = async () => {
      try {
        const list = await api<{ businesses: BusinessDTO[] }>('/businesses')
        const editable = list.businesses
          .filter((b) => b.status === 'draft' || b.status === 'rejected')
          .sort((a, b2) => b2.updated_at.localeCompare(a.updated_at))[0]
        if (editable) {
          hydrate(editable)
          return
        }
        const r = await api<{ business: BusinessDTO }>('/businesses', { method: 'POST' })
        hydrate(r.business)
      } catch (e) {
        setError(e instanceof ApiError ? e.message : 'Could not start the wizard.')
      }
    }
    void init()
  }, [])

  // Load documents of the resumed draft.
  const { data: existingDocs } = useQuery({
    queryKey: ['docs', business?.id],
    queryFn: () => api<{ documents: VerificationDocumentDTO[] }>(`/businesses/${business!.id}/documents`),
    enabled: !!business,
  })
  useEffect(() => {
    if (existingDocs) setDocs(existingDocs.documents)
  }, [existingDocs])

  const patch = async (body: Record<string, unknown>, silent = false) => {
    if (!business) return null
    const r = await api<{ business: BusinessDTO }>(`/businesses/${business.id}`, { method: 'PATCH', body })
    setBusiness(r.business)
    if (!silent) {
      setSavedFlash(true)
      setTimeout(() => setSavedFlash(false), 1500)
    }
    return r.business
  }

  const saveStep = async (next: number) => {
    setError('')
    setSaving(true)
    try {
      const payload: Record<string, unknown> = {}
      if (step === 0) {
        payload.name = form.name
        payload.tagline = form.tagline
        payload.description = form.description
        payload.category_id = form.category_id
        if (logo) payload.logo_url = logo.url
        if (cover) payload.cover_url = cover.url
        if (!form.name.trim() || !form.description.trim() || form.description.trim().length < 50) {
          throw new ApiError('validation_error', 'Name is required and description must be at least 50 characters.', 400, {})
        }
      }
      if (step === 1) {
        payload.address = form.address
        payload.city = form.city
        payload.country = form.country
        payload.timezone = form.timezone || 'UTC'
        payload.lat = form.lat ? Number(form.lat) : 0
        payload.lng = form.lng ? Number(form.lng) : 0
        if (!form.address.trim() || !form.lat || !form.lng) {
          throw new ApiError('validation_error', 'Address and coordinates are required. Use "Use my location" to pin.', 400, {})
        }
      }
      if (step === 2) {
        payload.contact = {
          phone: form.phone, email: form.email, website: form.website, whatsapp: form.whatsapp,
          instagram: form.instagram, tiktok: form.tiktok, facebook: form.facebook, x: form.x,
          youtube: form.youtube, line: form.line, telegram: form.telegram,
        }
      }
      if (step === 3) payload.hours = hours
      if (Object.keys(payload).length > 0) {
        await patch(payload)
      }
      setStep(next)
    } catch (e) {
      setError(e instanceof ApiError ? (Object.values(e.fields ?? {}).join(' ') || e.message) : 'Save failed.')
    } finally {
      setSaving(false)
    }
  }

  const upload = async (kind: 'logo' | 'cover' | 'document_verification', file: File, docKind?: string) => {
    setUploading(true)
    setError('')
    try {
      const r = await uploadMedia(kind, file)
      if (kind === 'logo') setLogo(r.media)
      if (kind === 'cover') setCover(r.media)
      if (kind === 'document_verification' && business && docKind) {
        const d = await api<{ document: VerificationDocumentDTO }>(
          `/businesses/${business.id}/documents`,
          { method: 'POST', body: { kind: docKind, media_id: r.media.id } },
        )
        setDocs((prev) => [d.document, ...prev])
      }
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Upload failed.')
    } finally {
      setUploading(false)
    }
  }

  const removeDoc = async (docId: string) => {
    if (!business) return
    await api(`/businesses/${business.id}/documents/${docId}`, { method: 'DELETE' })
    setDocs((prev) => prev.filter((d) => d.id !== docId))
  }

  const submit = async () => {
    if (!business) return
    setError('')
    setSaving(true)
    try {
      await api(`/businesses/${business.id}/submit`, { method: 'POST' })
      navigate('/dashboard/settings/verification', { state: { justSubmitted: true } })
    } catch (e) {
      setError(e instanceof ApiError ? (Object.values(e.fields ?? {}).join(' ') || e.message) : 'Submission failed.')
    } finally {
      setSaving(false)
    }
  }

  const useMyLocation = () => {
    if (!navigator.geolocation) {
      setError('Geolocation is not available. Enter coordinates manually.')
      return
    }
    navigator.geolocation.getCurrentPosition(
      (pos) => setForm((f) => ({ ...f, lat: pos.coords.latitude.toFixed(5), lng: pos.coords.longitude.toFixed(5) })),
      () => setError('Could not get location. Allow access or enter coordinates manually.'),
    )
  }

  if (!business) return <PageSpinner label="Starting your storefront draft…" />

  const input = (key: string) => ({
    value: form[key],
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => setForm((f) => ({ ...f, [key]: e.target.value })),
  })

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-8">
        <p className="mono-label mb-1">Business registration</p>
        <h1 className="text-2xl font-semibold tracking-tight">Create your storefront</h1>
        <p className="mt-1 text-sm text-ink2">6 steps · auto-saved as a draft · free forever</p>
      </div>

      {/* Step indicator */}
      <ol className="mb-8 flex flex-wrap items-center gap-1 text-xs">
        {STEPS.map((s, i) => (
          <li key={s} className="flex items-center gap-1">
            <button
              onClick={() => i < step && setStep(i)}
              className={`flex items-center gap-1.5 rounded-full px-2.5 py-1 font-medium transition-colors ${
                i === step ? 'bg-accent text-accent-ink' : i < step ? 'text-ink hover:bg-surface2' : 'text-ink3'
              }`}
            >
              {i < step ? <CheckCircle2 className="h-3 w-3" /> : <span>{i + 1}</span>}
              {s}
            </button>
            {i < STEPS.length - 1 && <span className="text-ink3">·</span>}
          </li>
        ))}
      </ol>

      {savedFlash && (
        <p className="mb-4 flex items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink2">
          <CheckCircle2 className="h-4 w-4 text-ink" /> Draft saved
        </p>
      )}
      {error && (
        <p className="mb-4 rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
          {error}
        </p>
      )}

      {step === 0 && (
        <Card className="space-y-4">
          <Input label="Business name" required placeholder="e.g. Rumah Kopi Senja" {...input('name')} />
          <Input label="Tagline" placeholder="One line that captures it" {...input('tagline')} />
          <div>
            <label className="mb-1.5 block text-sm font-medium text-ink">Category</label>
            <select
              value={form.category_id}
              onChange={(e) => setForm((f) => ({ ...f, category_id: e.target.value }))}
              className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink focus:border-ink"
            >
              <option value="">Pick a sub-category…</option>
              {leafCategories.map((c) => (
                <option key={c.id} value={c.id}>{c.name}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="mb-1.5 block text-sm font-medium text-ink">Description</label>
            <textarea
              rows={5}
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              placeholder="Tell people what makes this place special. At least 50 characters."
              className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink"
            />
            <p className="mt-1 text-xs text-ink3">{form.description.trim().length}/50 minimum</p>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <UploadField label="Logo (required)" media={logo} onFile={(f) => void upload('logo', f)} uploading={uploading} />
            <UploadField label="Cover (optional)" media={cover} onFile={(f) => void upload('cover', f)} uploading={uploading} />
          </div>
        </Card>
      )}

      {step === 1 && (
        <Card className="space-y-4">
          <Input label="Address" placeholder="Street, building, area" {...input('address')} />
          <div className="grid grid-cols-2 gap-4">
            <Input label="City" {...input('city')} />
            <Input label="Country" {...input('country')} />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <Input label="Latitude" placeholder="-6.20000" {...input('lat')} />
            <Input label="Longitude" placeholder="106.81660" {...input('lng')} />
          </div>
          <div>
            <label className="mb-1.5 block text-sm font-medium text-ink">Timezone</label>
            <select
              value={form.timezone ?? 'UTC'}
              onChange={(e) => setForm((f) => ({ ...f, timezone: e.target.value }))}
              className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink"
            >
              {TIMEZONES.map((tz) => <option key={tz} value={tz}>{tz}</option>)}
            </select>
            <p className="mt-1 text-xs text-ink3">Used for "Open now".</p>
          </div>
          <Button variant="secondary" onClick={useMyLocation}>
            <MapPin className="h-4 w-4" /> Use my location
          </Button>
        </Card>
      )}

      {step === 2 && (
        <Card className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <Input label="Phone" type="tel" placeholder="+62 21 555 0101" {...input('phone')} />
            <Input label="Email" type="email" placeholder="hello@business.com" {...input('email')} />
            <Input label="Website" placeholder="https://…" {...input('website')} />
            <Input label="WhatsApp" placeholder="+62 812 555 0101" {...input('whatsapp')} />
          </div>
          <div className="border-t border-border pt-4">
            <p className="mono-label mb-3">Social media</p>
            <div className="grid gap-4 sm:grid-cols-2">
              {SOCIAL_FIELDS.map(([key, label]) => (
                <Input key={key} label={label} placeholder={`${label} handle`} {...input(key)} />
              ))}
            </div>
          </div>
        </Card>
      )}

      {step === 3 && (
        <Card className="space-y-3">
          {DAYS.map(([key, label]) => {
            const d = hours[key]
            return (
              <div key={key} className="flex items-center gap-3">
                <label className="flex w-28 shrink-0 items-center gap-2 text-sm text-ink">
                  <input
                    type="checkbox"
                    checked={!d.closed}
                    onChange={(e) => setHours((h) => ({ ...h, [key]: { ...h[key], closed: !e.target.checked } }))}
                    className="h-4 w-4 accent-black dark:accent-white"
                  />
                  {label}
                </label>
                {!d.closed && (
                  <div className="flex items-center gap-2">
                    <input
                      type="time"
                      value={d.open}
                      onChange={(e) => setHours((h) => ({ ...h, [key]: { ...h[key], open: e.target.value } }))}
                      className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink"
                    />
                    <span className="text-ink3">–</span>
                    <input
                      type="time"
                      value={d.close}
                      onChange={(e) => setHours((h) => ({ ...h, [key]: { ...h[key], close: e.target.value } }))}
                      className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink"
                    />
                  </div>
                )}
              </div>
            )
          })}
          <p className="pt-2 text-xs text-ink3">Open at least 5 days is required.</p>
        </Card>
      )}

      {step === 4 && (
        <Card className="space-y-5">
          <p className="text-sm text-ink2">
            Upload your business registration or license to qualify for the{' '}
            <span className="font-medium text-ink">Fully Verified</span> badge. Documents are
            encrypted and only visible to the platform admin.
          </p>
          <div className="grid gap-3 sm:grid-cols-2">
            {(Object.keys(DOC_KINDS) as (keyof typeof DOC_KINDS)[]).map((kind) => (
              <label
                key={kind}
                className={`flex cursor-pointer flex-col items-center gap-2 rounded-lg border border-dashed p-5 text-center transition-colors ${
                  docs.some((d) => d.kind === kind) ? 'border-ink bg-surface2' : 'border-border hover:bg-surface2'
                }`}
              >
                <CloudUpload className="h-5 w-5 text-ink2" />
                <span className="text-xs font-medium text-ink">{DOC_KINDS[kind]}</span>
                {docs.some((d) => d.kind === kind) && (
                  <span className="text-xs text-ink2">✓ uploaded</span>
                )}
                <input
                  type="file"
                  accept="image/jpeg,image/png,application/pdf"
                  className="hidden"
                  disabled={uploading}
                  onChange={(e) => {
                    const f = e.target.files?.[0]
                    resetFileInput(e)
                    if (f) void upload('document_verification', f, kind)
                  }}
                />
              </label>
            ))}
          </div>
          {docs.length > 0 && (
            <ul className="divide-y divide-border border-t border-border pt-3">
              {docs.map((d) => (
                <li key={d.id} className="flex items-center gap-2 py-2 text-sm">
                  <span className="flex-1 text-ink">{DOC_KINDS[d.kind] ?? d.kind}</span>
                  <span className="text-xs text-ink3">{d.file_name}</span>
                  <span className={`text-xs ${d.status === 'approved' ? 'text-ink' : 'text-ink3'}`}>{d.status}</span>
                  <button onClick={() => void removeDoc(d.id)} className="text-ink3 hover:text-ink" aria-label="Remove document">
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}

      {step === 5 && (
        <Card className="space-y-3">
          <ReviewRow label="Name" value={form.name} />
          <ReviewRow label="Category" value={leafCategories.find((c) => c.id === form.category_id)?.name ?? '—'} />
          <ReviewRow label="Location" value={`${form.address}, ${form.city}, ${form.country}`} />
          <ReviewRow label="Contact" value={[form.phone, form.email].filter(Boolean).join(' · ') || '—'} />
          <ReviewRow label="Open days" value={`${DAYS.filter(([k]) => !hours[k].closed).length}/7 days`} />
          <ReviewRow label="Logo" value={logo ? 'Uploaded ✓' : 'Missing — required'} />
          <ReviewRow label="Documents" value={`${docs.length} uploaded`} />
          <div className="flex items-center justify-between border-t border-border pt-4">
            <p className="text-sm text-ink3">Status: {business.status}</p>
            <Button onClick={() => void submit()} disabled={saving}>
              {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Submit for verification'}
            </Button>
          </div>
        </Card>
      )}

      <div className="mt-6 flex items-center justify-between">
        <Button variant="ghost" onClick={() => setStep((s) => Math.max(0, s - 1))} disabled={step === 0 || saving}>
          <ArrowLeft className="h-4 w-4" /> Back
        </Button>
        {step < 5 && (
          <Button onClick={() => void saveStep(step + 1)} disabled={saving}>
            {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Save & continue'} <ArrowRight className="h-4 w-4" />
          </Button>
        )}
      </div>
    </div>
  )
}

function ReviewRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 text-sm">
      <span className="text-ink3">{label}</span>
      <span className="max-w-[60%] truncate text-right font-medium text-ink">{value}</span>
    </div>
  )
}

function UploadField({
  label,
  media,
  uploading,
  onFile,
}: {
  label: string
  media: MediaDTO | null
  uploading: boolean
  onFile: (f: File) => void
}) {
  return (
    <div>
      <p className="mb-1.5 text-sm font-medium text-ink">{label}</p>
      <label
        className={`flex cursor-pointer items-center justify-center gap-2 rounded-lg border border-dashed p-4 text-sm transition-colors ${
          media ? 'border-ink bg-surface2' : 'border-border hover:bg-surface2'
        }`}
      >
        {uploading ? (
          <Loader2 className="h-4 w-4 animate-spin" />
        ) : media ? (
          <CheckCircle2 className="h-4 w-4" />
        ) : (
          <CloudUpload className="h-4 w-4" />
        )}
        {media ? 'Uploaded ✓' : 'Choose image'}
        <input type="file" accept="image/jpeg,image/png,image/webp" className="hidden" onChange={(e) => {
          const f = e.target.files?.[0]
          resetFileInput(e)
          if (f) onFile(f)
        }} />
      </label>
    </div>
  )
}
