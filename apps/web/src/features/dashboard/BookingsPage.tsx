import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CalendarDays, MapPin } from 'lucide-react'
import { api, type AppointmentDTO } from '@/lib/api'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Confirm } from '@/components/ui/Modal'
import { PageSpinner } from '@/components/ui/Spinner'
import { toast } from '@/components/ui/Toast'
import { usePageMeta } from '@/lib/meta'

/** An owner's calendar: who is coming, when, and whether it still needs action.
 *
 *  Times are rendered in the BUSINESS's timezone, which is the one the slot was
 *  booked in. The owner may be travelling; the shop is not.
 */
function when(tz: string | undefined, iso: string): string {
  return new Intl.DateTimeFormat('en-GB', {
    timeZone: tz || 'UTC',
    weekday: 'short',
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(iso))
}

export function BookingsPage() {
  usePageMeta('Bookings')
  const business = useActiveBusiness()
  const qc = useQueryClient()
  const [cancelTarget, setCancelTarget] = useState<AppointmentDTO | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['dashboard-bookings', business?.id],
    queryFn: () => api<{ appointments: AppointmentDTO[] }>(`/businesses/${business!.id}/appointments`),
    enabled: !!business,
  })

  const refresh = () => qc.invalidateQueries({ queryKey: ['dashboard-bookings', business?.id] })

  const setStatus = useMutation({
    mutationFn: ({ id, status }: { id: string; status: 'confirmed' | 'completed' | 'no_show' }) =>
      api(`/appointments/${id}/status`, { method: 'PATCH', body: { status } }),
    onSuccess: (_r, v) => {
      refresh()
      toast.success(v.status === 'confirmed' ? 'Booking confirmed' : `Marked ${v.status.replace('_', '-')}`)
    },
    onError: (e) => toast.error((e as Error).message || 'Could not update the booking.'),
  })

  const cancel = useMutation({
    mutationFn: (a: AppointmentDTO) => api(`/appointments/${a.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      refresh()
      toast.success('Booking cancelled')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not cancel the booking.'),
  })

  const { upcoming, past } = useMemo(() => {
    const now = Date.now()
    const all = data?.appointments ?? []
    const live = (a: AppointmentDTO) => a.status === 'pending' || a.status === 'confirmed'
    return {
      upcoming: all.filter((a) => live(a) && new Date(a.ends_at).getTime() >= now),
      past: all.filter((a) => !live(a) || new Date(a.ends_at).getTime() < now),
    }
  }, [data])

  if (!business) {
    return (
      <Card>
        <p className="text-sm text-ink3">Register a business first — bookings appear here once you have one.</p>
      </Card>
    )
  }
  if (isLoading) return <PageSpinner />

  const row = (a: AppointmentDTO, open: boolean) => {
    const started = new Date(a.starts_at).getTime() <= Date.now()
    return (
      <Card key={a.id} className="p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="text-sm font-semibold text-ink">
              {a.customer_name || 'Customer'}
              <span className="ml-2 text-xs font-normal text-ink3">{a.product_name}</span>
            </p>
            <p className="mt-2 flex items-center gap-1.5 text-sm text-ink">
              <CalendarDays className="h-3.5 w-3.5 text-accent-ink" aria-hidden="true" />
              {when(a.business_timezone, a.starts_at)}
            </p>
            {a.location_name && (
              <p className="mt-0.5 flex items-center gap-1 text-xs text-ink3">
                <MapPin className="h-3 w-3" aria-hidden="true" />
                {a.location_name}
              </p>
            )}
            {a.notes && <p className="mt-2 text-xs text-ink2">{a.notes}</p>}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="rounded-full border border-border px-2 py-0.5 text-[10px] font-medium text-ink2">
              {a.status.replace('_', ' ')}
            </span>
            {open && a.status === 'pending' && (
              <Button size="sm" onClick={() => setStatus.mutate({ id: a.id, status: 'confirmed' })} disabled={setStatus.isPending}>
                Confirm
              </Button>
            )}
            {open && a.status === 'confirmed' && started && (
              <>
                <Button size="sm" onClick={() => setStatus.mutate({ id: a.id, status: 'completed' })} disabled={setStatus.isPending}>
                  Completed
                </Button>
                <Button size="sm" variant="secondary" onClick={() => setStatus.mutate({ id: a.id, status: 'no_show' })} disabled={setStatus.isPending}>
                  No-show
                </Button>
              </>
            )}
            {/* Cancelling a slot that already started is refused by the API: it
                would rewrite history and free a time that has passed. */}
            {open && !started && (
              <Button size="sm" variant="ghost" onClick={() => setCancelTarget(a)}>
                Cancel
              </Button>
            )}
          </div>
        </div>
      </Card>
    )
  }

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-6">
        <p className="mono-label mb-1">Calendar</p>
        <h1 className="text-2xl font-semibold tracking-tight">Bookings</h1>
        <p className="mt-1 text-sm text-ink2">Times are shown in the business's timezone.</p>
      </div>

      {upcoming.length === 0 && past.length === 0 && (
        <Card>
          <p className="text-sm text-ink3">No bookings yet.</p>
        </Card>
      )}

      {upcoming.length > 0 && (
        <section aria-labelledby="upcoming" className="mb-8">
          <h2 id="upcoming" className="mono-label mb-2">Upcoming</h2>
          <div className="space-y-3">{upcoming.map((a) => row(a, true))}</div>
        </section>
      )}

      {past.length > 0 && (
        <section aria-labelledby="past">
          <h2 id="past" className="mono-label mb-2">Past</h2>
          <div className="space-y-3">{past.map((a) => row(a, false))}</div>
        </section>
      )}

      <Confirm
        open={!!cancelTarget}
        title="Cancel this booking?"
        message="The customer will be told. This cannot be undone."
        confirmLabel="Cancel booking"
        danger
        onClose={() => setCancelTarget(null)}
        onConfirm={() => {
          if (cancelTarget) void cancel.mutateAsync(cancelTarget)
          setCancelTarget(null)
        }}
      />
    </div>
  )
}
