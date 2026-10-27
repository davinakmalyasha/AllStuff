import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { MapPin, Star, Trash2 } from 'lucide-react'
import { api, type LocationDTO } from '@/lib/api'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Input } from '@/components/ui/Input'
import { Confirm, Modal } from '@/components/ui/Modal'
import { PageSpinner } from '@/components/ui/Spinner'
import { toast } from '@/components/ui/Toast'
import { usePageMeta } from '@/lib/meta'

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

  const { data, isLoading } = useQuery({
    queryKey: ['locations', business?.id],
    queryFn: () => api<{ locations: LocationDTO[] }>(`/businesses/${business!.id}/locations`),
    enabled: !!business,
  })
  const locations = data?.locations ?? []
  const refresh = () => qc.invalidateQueries({ queryKey: ['locations', business?.id] })

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
