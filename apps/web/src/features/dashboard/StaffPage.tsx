import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CalendarClock, Pencil, Plus, Trash2, UserCheck, UserX } from 'lucide-react'
import { api, type DayHoursDTO, type ProductDTO, type StaffDTO } from '@/lib/api'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Input } from '@/components/ui/Input'
import { Confirm, Modal } from '@/components/ui/Modal'
import { PageSpinner } from '@/components/ui/Spinner'
import { toast } from '@/components/ui/Toast'
import { usePageMeta } from '@/lib/meta'
import { HoursEditor } from './HoursEditor'

type Draft = { name: string; email: string; phone: string }
const EMPTY: Draft = { name: '', email: '', phone: '' }

/**
 * The people who perform services.
 *
 * Qualification is explicit: a staff member with no services assigned is
 * bookable for nothing, not for everything. The screen says that where an owner
 * would otherwise assume the opposite, because "no checkboxes ticked" reading as
 * "all of them" is how a shop ends up offering a service nobody can perform.
 */
export function StaffPage() {
  usePageMeta('Staff')
  const business = useActiveBusiness()
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<StaffDTO | null>(null)
  const [draft, setDraft] = useState<Draft>(EMPTY)
  const [servicesFor, setServicesFor] = useState<StaffDTO | null>(null)
  const [picked, setPicked] = useState<string[]>([])
  const [hoursFor, setHoursFor] = useState<StaffDTO | null>(null)
  const [hours, setHours] = useState<Record<string, DayHoursDTO>>({})
  const [deleteTarget, setDeleteTarget] = useState<StaffDTO | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['staff', business?.id],
    queryFn: () => api<{ staff: StaffDTO[] }>(`/businesses/${business!.id}/staff`),
    enabled: !!business,
  })
  const { data: productsData } = useQuery({
    queryKey: ['products', business?.id],
    queryFn: () => api<{ products: ProductDTO[] }>(`/businesses/${business!.id}/products`),
    enabled: !!business,
  })

  const staff = data?.staff ?? []
  const bookable = useMemo(
    () => (productsData?.products ?? []).filter((p) => p.type === 'service'),
    [productsData],
  )
  const nameOf = useMemo(() => {
    const m = new Map(bookable.map((p) => [p.id, p.name]))
    return (id: string) => m.get(id) ?? 'a service that no longer exists'
  }, [bookable])

  // A newly-enabled day starts at the business's first opening hours, not at a
  // hard-coded 09:00 that may sit outside the shop's day entirely.
  const defaults = useMemo(() => {
    const hours = business?.hours ?? {}
    for (const key of ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']) {
      const h = hours[key]
      if (h?.open && h?.close) return { opens_at: h.open, closes_at: h.close }
    }
    return { opens_at: '09:00', closes_at: '17:00' }
  }, [business])

  const refresh = () => qc.invalidateQueries({ queryKey: ['staff', business?.id] })

  const saveStaff = useMutation({
    mutationFn: () => {
      const body = {
        name: draft.name.trim(),
        email: draft.email.trim(),
        phone: draft.phone.trim(),
      }
      if (editing) return api(`/businesses/${business!.id}/staff/${editing.id}`, { method: 'PATCH', body })
      return api(`/businesses/${business!.id}/staff`, { method: 'POST', body })
    },
    onSuccess: () => {
      refresh()
      setCreating(false)
      setEditing(null)
      setDraft(EMPTY)
      toast.success(editing ? 'Staff member updated' : 'Staff member added')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not save the staff member.'),
  })

  const setActive = useMutation({
    mutationFn: (st: StaffDTO) =>
      api(`/businesses/${business!.id}/staff/${st.id}`, { method: 'PATCH', body: { active: !st.active } }),
    onSuccess: (_r, st) => {
      refresh()
      toast.success(st.active ? `${st.name} is no longer taking bookings` : `${st.name} is taking bookings again`)
    },
    onError: (e) => toast.error((e as Error).message || 'Could not change the staff member.'),
  })

  const saveServices = useMutation({
    mutationFn: (st: StaffDTO) =>
      api(`/businesses/${business!.id}/staff/${st.id}/services`, { method: 'PUT', body: { product_ids: picked } }),
    onSuccess: () => {
      refresh()
      setServicesFor(null)
      toast.success('Services updated')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not update the services.'),
  })

  const saveHours = useMutation({
    mutationFn: (st: StaffDTO) =>
      api(`/businesses/${business!.id}/staff/${st.id}/hours`, { method: 'PUT', body: { hours } }),
    onSuccess: () => {
      refresh()
      setHoursFor(null)
      toast.success('Shift updated')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not update the shift.'),
  })

  const remove = useMutation({
    mutationFn: (st: StaffDTO) => api(`/businesses/${business!.id}/staff/${st.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      refresh()
      toast.success('Staff member removed')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not remove the staff member.'),
  })

  if (!business) {
    return (
      <Card>
        <p className="text-sm text-ink3">Register a business first — staff belong to one.</p>
      </Card>
    )
  }
  if (isLoading) return <PageSpinner />

  const openCreate = () => {
    setDraft(EMPTY)
    setEditing(null)
    setCreating(true)
  }
  const openEdit = (st: StaffDTO) => {
    setCreating(false)
    setEditing(st)
    setDraft({ name: st.name, email: st.email ?? '', phone: st.phone ?? '' })
  }
  const openServices = (st: StaffDTO) => {
    setPicked(st.services ?? [])
    setServicesFor(st)
  }
  const openHours = (st: StaffDTO) => {
    setHours(st.hours ?? {})
    setHoursFor(st)
  }

  const shiftSummary = (st: StaffDTO) => {
    const h = st.hours ?? {}
    const working = Object.keys(h).filter((d) => h[d].working).length
    const off = Object.keys(h).filter((d) => !h[d].working).length
    if (working === 0 && off === 0) return 'Same as the business'
    const parts = [`${working} working ${working === 1 ? 'day' : 'days'}`]
    if (off > 0) parts.push(`${off} off`)
    return parts.join(', ')
  }

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-6 flex items-start justify-between gap-3">
        <div>
          <p className="mono-label mb-1">Team</p>
          <h1 className="text-2xl font-semibold tracking-tight">Staff</h1>
          <p className="mt-1 text-sm text-ink2">
            Only active people who perform a service can be booked.
          </p>
        </div>
        <Button onClick={openCreate}>
          <Plus className="h-4 w-4" aria-hidden="true" />
          Add staff
        </Button>
      </div>

      {staff.length === 0 && (
        <Card>
          <p className="text-sm text-ink3">
            No staff yet. If the diary is just you, add yourself — bookings need someone to hold them.
          </p>
        </Card>
      )}

      <div className="space-y-3">
        {staff.map((st) => {
          const services = st.services ?? []
          return (
            <Card key={st.id} className="p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="flex items-center gap-1.5 text-sm font-semibold text-ink">
                    {st.name}
                    <Badge tone={st.active ? 'positive' : 'neutral'} dot>
                      {st.active ? 'Active' : 'Inactive'}
                    </Badge>
                  </p>
                  {(st.email || st.phone) && (
                    <p className="mt-0.5 text-xs text-ink3">{[st.email, st.phone].filter(Boolean).join(' · ')}</p>
                  )}
                  <p className="mt-2 text-xs text-ink2">
                    {services.length === 0 ? (
                      <span className="text-ink3">Not qualified for any service — not bookable yet.</span>
                    ) : (
                      services.map(nameOf).join(', ')
                    )}
                  </p>
                  <p className="mt-1 flex items-center gap-1 text-xs text-ink3">
                    <CalendarClock className="h-3 w-3" aria-hidden="true" />
                    {shiftSummary(st)}
                  </p>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Button size="sm" variant="secondary" onClick={() => openServices(st)}>
                    Services
                  </Button>
                  <Button size="sm" variant="secondary" onClick={() => openHours(st)}>
                    Shift
                  </Button>
                  <Button size="sm" variant="ghost" aria-label={`Edit ${st.name}`} onClick={() => openEdit(st)}>
                    <Pencil className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => setActive.mutate(st)}
                    disabled={setActive.isPending}
                    aria-label={st.active ? `Deactivate ${st.name}` : `Activate ${st.name}`}
                  >
                    {st.active ? <UserX className="h-3.5 w-3.5" /> : <UserCheck className="h-3.5 w-3.5" />}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`Remove ${st.name}`}
                    onClick={() => setDeleteTarget(st)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </div>
              </div>
            </Card>
          )
        })}
      </div>

      <Modal
        open={creating || !!editing}
        onClose={() => {
          setCreating(false)
          setEditing(null)
        }}
        title={editing ? 'Edit staff member' : 'Add a staff member'}
      >
        <div className="space-y-3">
          <Input label="Name" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
          <Input
            label="Email"
            type="email"
            value={draft.email}
            onChange={(e) => setDraft({ ...draft, email: e.target.value })}
            hint="Optional. Used only to contact the person, not to give them an account."
          />
          <Input label="Phone" value={draft.phone} onChange={(e) => setDraft({ ...draft, phone: e.target.value })} />
          <div className="flex justify-end gap-2">
            <Button
              variant="secondary"
              onClick={() => {
                setCreating(false)
                setEditing(null)
              }}
            >
              Cancel
            </Button>
            <Button onClick={() => saveStaff.mutate()} disabled={saveStaff.isPending}>
              Save
            </Button>
          </div>
        </div>
      </Modal>

      <Modal
        open={!!servicesFor}
        onClose={() => setServicesFor(null)}
        title={servicesFor ? `Services for ${servicesFor.name}` : 'Services'}
      >
        <div className="space-y-3">
          {bookable.length === 0 ? (
            <p className="text-sm text-ink3">
              No services yet. Add one under Products, with a duration, before assigning it here.
            </p>
          ) : (
            <>
              <p className="text-xs text-ink3">
                Only ticked services can be booked with this person. Ticking nothing makes them unbookable —
                that is deliberate, so a new staff member cannot silently take every service.
              </p>
              <div className="space-y-2">
                {bookable.map((p) => (
                  <label key={p.id} className="flex items-center gap-2 text-sm text-ink">
                    <input
                      type="checkbox"
                      checked={picked.includes(p.id)}
                      onChange={(e) =>
                        setPicked((prev) => (e.target.checked ? [...prev, p.id] : prev.filter((id) => id !== p.id)))
                      }
                      className="h-4 w-4 rounded border-border"
                    />
                    {p.name}
                  </label>
                ))}
              </div>
            </>
          )}
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setServicesFor(null)}>
              Cancel
            </Button>
            <Button
              onClick={() => servicesFor && saveServices.mutate(servicesFor)}
              disabled={saveServices.isPending || bookable.length === 0}
            >
              Save
            </Button>
          </div>
        </div>
      </Modal>

      <Modal
        open={!!hoursFor}
        onClose={() => setHoursFor(null)}
        title={hoursFor ? `Shift for ${hoursFor.name}` : 'Shift'}
        maxWidth="max-w-2xl"
      >
        <div className="space-y-4">
          <p className="text-xs text-ink3">
            A working day narrows the bookable window to the hours given. A day off is not the same as
            inheriting: it stays off even when the business is open.
          </p>
          <HoursEditor value={hours} onChange={setHours} defaults={defaults} label="shift" />
          <div className="flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setHoursFor(null)}>
              Cancel
            </Button>
            <Button onClick={() => hoursFor && saveHours.mutate(hoursFor)} disabled={saveHours.isPending}>
              Save
            </Button>
          </div>
        </div>
      </Modal>

      <Confirm
        open={!!deleteTarget}
        title="Remove this staff member?"
        message="Their past bookings keep their history, but future ones can no longer be assigned to them."
        confirmLabel="Remove staff member"
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
