import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { MapPin, Star, Trash2 } from 'lucide-react'
import { api, type DayHoursDTO, type LocationDTO, type ProductDTO } from '@/lib/api'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Input } from '@/components/ui/Input'
import { Confirm, Modal } from '@/components/ui/Modal'
import { PageSpinner } from '@/components/ui/Spinner'
import { toast } from '@/components/ui/Toast'
import { usePageMeta } from '@/lib/meta'
import { HoursEditor } from './HoursEditor'

type Draft = {
  name: string
  address: string
  city: string
  country: string
  lat: string
  lng: string
  timezone: string
}

function draftFrom(loc: LocationDTO | null, fallbackTz: string): Draft {
  return {
    name: loc?.name ?? '',
    address: loc?.address ?? '',
    city: loc?.city ?? '',
    country: loc?.country ?? '',
    lat: loc ? String(loc.lat) : '',
    lng: loc ? String(loc.lng) : '',
    timezone: loc?.timezone || fallbackTz || 'UTC',
  }
}

/** A business's places. The PRIMARY one is what the listing's own address and
 *  hours mirror, so promoting a branch also changes what the public page and
 *  search show - the UI says so rather than letting an owner discover it. */
export function LocationsPage() {
  usePageMeta('Locations')
  const business = useActiveBusiness()
  const qc = useQueryClient()
  const [editing, setEditing] = useState<LocationDTO | 'new' | null>(null)
  const [draft, setDraft] = useState<Draft>(draftFrom(null, ''))
  const [deleteTarget, setDeleteTarget] = useState<LocationDTO | null>(null)
  const [hoursFor, setHoursFor] = useState<LocationDTO | null>(null)
  const [hoursProduct, setHoursProduct] = useState('')
  const [hours, setHours] = useState<Record<string, DayHoursDTO>>({})
  const seededFor = useRef<string | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['locations', business?.id],
    queryFn: () => api<{ locations: LocationDTO[] }>(`/businesses/${business!.id}/locations`),
    enabled: !!business,
  })
  const locations = data?.locations ?? []
  const refresh = () => qc.invalidateQueries({ queryKey: ['locations', business?.id] })

  const { data: productsData } = useQuery({
    queryKey: ['products', business?.id],
    queryFn: () => api<{ products: ProductDTO[] }>(`/businesses/${business!.id}/products`),
    enabled: !!business,
  })
  const services = useMemo(
    () => (productsData?.products ?? []).filter((p) => p.type === 'service'),
    [productsData],
  )

  // An empty map means "runs whenever the location does", so the editor must not
  // seed itself with the location's hours - that would turn inheritance into an
  // explicit narrow window the first time somebody pressed Save.
  const { data: savedHours } = useQuery({
    queryKey: ['service-hours', hoursFor?.id, hoursProduct],
    queryFn: () =>
      api<{ hours: Record<string, DayHoursDTO> | null }>(
        `/businesses/${business!.id}/locations/${hoursFor!.id}/service-hours?product_id=${hoursProduct}`,
      ),
    enabled: !!business && !!hoursFor && !!hoursProduct,
    staleTime: Infinity,
  })
  useEffect(() => {
    if (savedHours && seededFor.current !== hoursProduct) {
      setHours(savedHours.hours ?? {})
      seededFor.current = hoursProduct
    }
  }, [savedHours, hoursProduct])

  const saveHours = useMutation({
    mutationFn: () =>
      api(`/businesses/${business!.id}/locations/${hoursFor!.id}/service-hours/${hoursProduct}`, {
        method: 'PUT',
        body: { hours },
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['service-hours', hoursFor?.id, hoursProduct] })
      setHoursFor(null)
      toast.success('Service hours updated')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not update the service hours.'),
  })

  const openHours = (loc: LocationDTO) => {
    seededFor.current = null
    setHours({})
    setHoursProduct(services[0]?.id ?? '')
    setHoursFor(loc)
  }

  const save = useMutation({
    mutationFn: () => {
      const body = {
        name: draft.name.trim(),
        address: draft.address.trim(),
        city: draft.city.trim(),
        country: draft.country.trim(),
        lat: Number(draft.lat),
        lng: Number(draft.lng),
        timezone: draft.timezone.trim(),
      }
      if (editing === 'new') {
        return api(`/businesses/${business!.id}/locations`, { method: 'POST', body })
      }
      return api(`/businesses/${business!.id}/locations/${(editing as LocationDTO).id}`, { method: 'PATCH', body })
    },
    onSuccess: () => {
      refresh()
      qc.invalidateQueries({ queryKey: ['my-businesses'] })
      setEditing(null)
      toast.success('Location saved')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not save the location.'),
  })

  const promote = useMutation({
    mutationFn: (loc: LocationDTO) =>
      api(`/businesses/${business!.id}/locations/${loc.id}/primary`, { method: 'POST' }),
    onSuccess: () => {
      refresh()
      qc.invalidateQueries({ queryKey: ['my-businesses'] })
      toast.success('Primary location updated')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not change the primary location.'),
  })

  const remove = useMutation({
    mutationFn: (loc: LocationDTO) =>
      api(`/businesses/${business!.id}/locations/${loc.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      refresh()
      qc.invalidateQueries({ queryKey: ['my-businesses'] })
      toast.success('Location removed')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not remove the location.'),
  })

  if (!business) {
    return (
      <Card>
        <p className="text-sm text-ink3">Register a business first — locations belong to one.</p>
      </Card>
    )
  }
  if (isLoading) return <PageSpinner />

  const openForm = (loc: LocationDTO | 'new') => {
    setEditing(loc)
    setDraft(draftFrom(loc === 'new' ? null : loc, business.timezone ?? 'UTC'))
  }

  const locationDefaults = () => {
    const h = hoursFor?.hours ?? {}
    for (const key of ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']) {
      const day = h[key]
      if (day?.open && day?.close) return { opens_at: day.open, closes_at: day.close }
    }
    return { opens_at: '09:00', closes_at: '17:00' }
  }

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-6 flex items-start justify-between gap-3">
        <div>
          <p className="mono-label mb-1">Places</p>
          <h1 className="text-2xl font-semibold tracking-tight">Locations</h1>
          <p className="mt-1 text-sm text-ink2">
            The primary location's address and hours are what search and the public page show.
          </p>
        </div>
        <Button onClick={() => openForm('new')}>Add location</Button>
      </div>

      {locations.length === 0 && (
        <Card>
          <p className="text-sm text-ink3">
            No locations yet. A business starts with the address on its listing; add one to make it explicit.
          </p>
        </Card>
      )}

      <div className="space-y-3">
        {locations.map((loc) => (
          <Card key={loc.id} className="p-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="flex items-center gap-1.5 text-sm font-semibold text-ink">
                  <MapPin className="h-3.5 w-3.5 text-accent-ink" aria-hidden="true" />
                  {loc.name}
                  {loc.is_primary && (
                    <span className="rounded-full border border-border px-2 py-0.5 text-[10px] font-medium text-ink2">
                      Primary
                    </span>
                  )}
                </p>
                <p className="mt-1 text-xs text-ink3">
                  {loc.address}, {loc.city}, {loc.country} · {loc.timezone}
                </p>
              </div>
              <div className="flex items-center gap-2">
                {!loc.is_primary && (
                  <Button size="sm" variant="ghost" onClick={() => promote.mutate(loc)} disabled={promote.isPending}>
                    <Star className="h-3.5 w-3.5" aria-hidden="true" />
                    <span className="sr-only">Make {loc.name} the primary location</span>
                  </Button>
                )}
                <Button size="sm" variant="secondary" onClick={() => openHours(loc)}>
                  Service hours
                </Button>
                <Button size="sm" variant="secondary" onClick={() => openForm(loc)}>
                  Edit
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label={`Remove ${loc.name}`}
                  disabled={locations.length <= 1}
                  onClick={() => setDeleteTarget(loc)}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            </div>
          </Card>
        ))}
      </div>

      <Modal
        open={!!editing}
        onClose={() => setEditing(null)}
        title={editing === 'new' ? 'Add a location' : 'Edit location'}
      >
        <div className="space-y-3">
          <Input label="Name" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
          <Input label="Address" value={draft.address} onChange={(e) => setDraft({ ...draft, address: e.target.value })} />
          <div className="grid grid-cols-2 gap-3">
            <Input label="City" value={draft.city} onChange={(e) => setDraft({ ...draft, city: e.target.value })} />
            <Input label="Country" value={draft.country} onChange={(e) => setDraft({ ...draft, country: e.target.value })} />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <Input label="Latitude" type="number" step="any" value={draft.lat} onChange={(e) => setDraft({ ...draft, lat: e.target.value })} />
            <Input label="Longitude" type="number" step="any" value={draft.lng} onChange={(e) => setDraft({ ...draft, lng: e.target.value })} />
          </div>
          <Input
            label="Timezone"
            value={draft.timezone}
            onChange={(e) => setDraft({ ...draft, timezone: e.target.value })}
            hint="An IANA name such as Asia/Jakarta. Bookings use this timezone."
          />
          <p className="text-xs text-ink3">
            Opening hours are inherited from the listing when a location is created.
          </p>
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setEditing(null)}>Cancel</Button>
            <Button onClick={() => save.mutate()} disabled={save.isPending}>Save</Button>
          </div>
        </div>
      </Modal>

      <Modal
        open={!!hoursFor}
        onClose={() => setHoursFor(null)}
        title={hoursFor ? `Service hours at ${hoursFor.name}` : 'Service hours'}
        maxWidth="max-w-2xl"
      >
        <div className="space-y-4">
          {services.length === 0 ? (
            <p className="text-sm text-ink3">
              No services yet. Add one under Products, with a duration, to give it hours here.
            </p>
          ) : (
            <>
              <div>
                <label htmlFor="service-hours-product" className="mb-1.5 block text-sm font-medium text-ink">
                  Service
                </label>
                <select
                  id="service-hours-product"
                  value={hoursProduct}
                  onChange={(e) => {
                    seededFor.current = null
                    setHours({})
                    setHoursProduct(e.target.value)
                  }}
                  className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink"
                >
                  {services.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </div>
              <p className="text-xs text-ink3">
                Leave a day on Inherit to run whenever the location is open. A narrower window is for a
                service that only runs part of the day; Off stops it being offered on that day entirely.
              </p>
              <HoursEditor value={hours} onChange={setHours} defaults={locationDefaults()} label="service" />
              <div className="flex justify-end gap-2">
                <Button variant="secondary" onClick={() => setHoursFor(null)}>
                  Cancel
                </Button>
                <Button onClick={() => saveHours.mutate()} disabled={saveHours.isPending || !hoursProduct}>
                  Save
                </Button>
              </div>
            </>
          )}
        </div>
      </Modal>

      <Confirm
        open={!!deleteTarget}
        title="Remove this location?"
        message="Bookings keep their history. If this was the primary location, another one is promoted."
        confirmLabel="Remove location"
        danger
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (deleteTarget) void remove.mutateAsync(deleteTarget)
          setDeleteTarget(null)
        }}
      />
    </div>
  )
}
