"use client"

import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CalendarDays, ChevronLeft, ChevronRight, MapPin } from 'lucide-react'
import { api, type AppointmentDTO } from '@/lib/api'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Confirm, Modal } from '@/components/ui/Modal'
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

/** Calendar date in the shop's timezone, as YYYY-MM-DD. en-CA orders the parts
 *  that way, which avoids assembling them by hand. */
function dayIn(tz: string | undefined, d: Date = new Date()): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: tz || 'UTC',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(d)
}

function addDays(day: string, n: number): string {
  const d = new Date(`${day}T12:00:00Z`)
  d.setUTCDate(d.getUTCDate() + n)
  return d.toISOString().slice(0, 10)
}

/** Minutes since midnight in the shop's timezone. Reading the UTC clock would
 *  place a 09:00 Jakarta appointment at 02:00 on the grid. */
function minutesIn(tz: string | undefined, iso: string): number {
  const parts = new Intl.DateTimeFormat('en-GB', {
    timeZone: tz || 'UTC',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).formatToParts(new Date(iso))
  const h = Number(parts.find((p) => p.type === 'hour')?.value ?? 0)
  const m = Number(parts.find((p) => p.type === 'minute')?.value ?? 0)
  return h * 60 + m
}

const GRID_START = 7 * 60
const GRID_END = 21 * 60

const STATUS_TONE: Record<AppointmentDTO['status'], string> = {
  pending: 'border-amber-400 bg-amber-50 text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200',
  confirmed: 'border-blue-400 bg-blue-50 text-blue-900 dark:border-blue-700 dark:bg-blue-950/40 dark:text-blue-200',
  completed: 'border-emerald-400 bg-emerald-50 text-emerald-900 dark:border-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-200',
  no_show: 'border-red-400 bg-red-50 text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200',
  cancelled: 'border-border bg-surface2 text-ink3',
}

export function BookingsPage() {
  usePageMeta('Bookings')
  const business = useActiveBusiness()
  const qc = useQueryClient()
  const [cancelTarget, setCancelTarget] = useState<AppointmentDTO | null>(null)
  const [selected, setSelected] = useState<AppointmentDTO | null>(null)
  const [view, setView] = useState<'list' | 'day'>('list')
  // Empty until the active business (and its timezone) has loaded: the shop's
  // today is not the viewer's UTC today, and defaulting to UTC opened the day
  // view on the wrong date for every Jakarta owner.
  const [day, setDay] = useState('')
  const dayInit = useRef(false)
  useEffect(() => {
    if (!dayInit.current && business) {
      setDay(dayIn(business.timezone))
      dayInit.current = true
    }
  }, [business])

  const { data, isLoading } = useQuery({
    queryKey: ['dashboard-bookings', business?.id],
    queryFn: () => api<{ appointments: AppointmentDTO[] }>(`/businesses/${business!.id}/appointments`),
    enabled: !!business,
  })

  // The day view asks for a slightly wider window than the shown date and
  // filters by the shop's calendar day: an instant at local midnight is not a
  // UTC midnight, and computing that boundary is exactly the kind of off-by-one
  // the list view avoids by never slicing days.
  const dayQuery = useQuery({
    queryKey: ['dashboard-bookings-day', business?.id, day],
    queryFn: () =>
      api<{ appointments: AppointmentDTO[] }>(
        `/businesses/${business!.id}/appointments?from=${addDays(day, -1)}T00:00:00Z&to=${addDays(day, 2)}T00:00:00Z`,
      ),
    enabled: !!business && view === 'day' && !!day,
  })

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['dashboard-bookings', business?.id] })
    void qc.invalidateQueries({ queryKey: ['dashboard-bookings-day', business?.id] })
  }

  const setStatus = useMutation({
    mutationFn: ({ id, status }: { id: string; status: 'confirmed' | 'completed' | 'no_show' }) =>
      api(`/appointments/${id}/status`, { method: 'PATCH', body: { status } }),
    onSuccess: (_r, v) => {
      refresh()
      setSelected(null)
      toast.success(v.status === 'confirmed' ? 'Booking confirmed' : `Marked ${v.status.replace('_', '-')}`)
    },
    onError: (e) => toast.error((e as Error).message || 'Could not update the booking.'),
  })

  const cancel = useMutation({
    mutationFn: (a: AppointmentDTO) => api(`/appointments/${a.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      refresh()
      setSelected(null)
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

  const dayAppointments = useMemo(() => {
    const all = dayQuery.data?.appointments ?? []
    return all
      .filter((a) => a.status !== 'cancelled' && dayIn(business?.timezone, new Date(a.starts_at)) === day)
      .sort((a, b) => a.starts_at.localeCompare(b.starts_at))
  }, [dayQuery.data, day, business?.timezone])

  const columns = useMemo(() => {
    const names = Array.from(new Set(dayAppointments.map((a) => a.staff_name || 'Unassigned')))
    return names.length > 0 ? names.sort() : ['Unassigned']
  }, [dayAppointments])

  const tz = business?.timezone

  // The grid opens on a working day (07:00-21:00) but stretches to cover what
  // is actually booked: a 03:00 slot clamped into a 07:00 grid looked like a
  // 07:00 booking, which is worse than a taller grid.
  const { gridStart, gridEnd, gridSpan } = useMemo(() => {
    let start = GRID_START
    let end = GRID_END
    for (const a of dayAppointments) {
      const s = minutesIn(tz, a.starts_at)
      let e = minutesIn(tz, a.ends_at)
      if (e <= s) e = 24 * 60
      start = Math.min(start, s)
      end = Math.max(end, e)
    }
    start = Math.floor(start / 60) * 60
    end = Math.ceil(end / 60) * 60
    return { gridStart: start, gridEnd: end, gridSpan: Math.max(60, end - start) }
  }, [dayAppointments, tz])

  const dayLabel = day
    ? new Intl.DateTimeFormat('en-GB', {
        timeZone: tz || 'UTC',
        weekday: 'long',
        day: 'numeric',
        month: 'long',
      }).format(new Date(`${day}T12:00:00Z`))
    : ''

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
    <div className={view === 'day' ? 'mx-auto max-w-5xl' : 'mx-auto max-w-2xl'}>
      <div className="mb-6 flex flex-wrap items-end justify-between gap-3">
        <div>
          <p className="mono-label mb-1">Calendar</p>
          <h1 className="text-2xl font-semibold tracking-tight">Bookings</h1>
          <p className="mt-1 text-sm text-ink2">Times are shown in the business's timezone.</p>
        </div>
        <div className="flex rounded-lg border border-border p-0.5" role="group" aria-label="Calendar view">
          {(['list', 'day'] as const).map((v) => (
            <button
              key={v}
              onClick={() => setView(v)}
              aria-pressed={view === v}
              className={`rounded-md px-3 py-1.5 text-xs capitalize ${view === v ? 'bg-accent text-accent-ink' : 'text-ink2'}`}
            >
              {v}
            </button>
          ))}
        </div>
      </div>

      {view === 'day' && (
        <>
          <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
            <div className="flex items-center gap-2">
              <Button size="sm" variant="ghost" aria-label="Previous day" disabled={!day} onClick={() => setDay(addDays(day, -1))}>
                <ChevronLeft className="h-4 w-4" />
              </Button>
              <p className="min-w-52 text-center text-sm font-medium text-ink" aria-live="polite">{dayLabel}</p>
              <Button size="sm" variant="ghost" aria-label="Next day" disabled={!day} onClick={() => setDay(addDays(day, 1))}>
                <ChevronRight className="h-4 w-4" />
              </Button>
              <Button size="sm" variant="secondary" onClick={() => setDay(dayIn(tz))}>
                Today
              </Button>
            </div>
            <p className="text-xs text-ink3">{dayAppointments.length} booking{dayAppointments.length === 1 ? '' : 's'}</p>
          </div>

          {dayQuery.isLoading && <PageSpinner />}
          <Card className="overflow-x-auto p-0">
            <div className="flex min-w-[36rem]">
              <div className="w-14 shrink-0 border-r border-border">
                <div className="h-10 border-b border-border" />
                {Array.from({ length: (gridEnd - gridStart) / 60 + 1 }, (_, i) => (
                  <div key={i} className="relative h-14 border-b border-border/60">
                    <span className="absolute -top-2 right-1 font-mono text-[10px] text-ink3">
                      {String((gridStart + i * 60) / 60).padStart(2, '0')}:00
                    </span>
                  </div>
                ))}
              </div>
              {columns.map((col) => (
                <div key={col} className="min-w-0 flex-1 border-r border-border last:border-r-0">
                  <div className="flex h-10 items-center justify-center border-b border-border text-xs font-medium text-ink2">
                    {col}
                  </div>
                  <div className="relative" style={{ height: `${((gridEnd - gridStart) / 60) * 56}px` }}>
                    {Array.from({ length: (gridEnd - gridStart) / 60 }, (_, i) => (
                      <div key={i} className="h-14 border-b border-border/60" />
                    ))}
                    {dayAppointments
                      .filter((a) => (a.staff_name || 'Unassigned') === col)
                      .map((a) => {
                        const start = Math.max(gridStart, minutesIn(tz, a.starts_at))
                        let end = minutesIn(tz, a.ends_at)
                        if (end <= start) end = 24 * 60
                        end = Math.min(gridEnd, end)
                        const top = ((start - gridStart) / gridSpan) * 100
                        const height = Math.max(3, ((end - start) / gridSpan) * 100)
                        return (
                          <button
                            key={a.id}
                            onClick={() => setSelected(a)}
                            className={`absolute left-1 right-1 overflow-hidden rounded-md border px-2 py-1 text-left text-[11px] leading-tight shadow-sm transition-shadow hover:shadow-card ${STATUS_TONE[a.status]}`}
                            style={{ top: `${top}%`, height: `${height}%` }}
                            title={`${when(tz, a.starts_at)} · ${a.customer_name} · ${a.product_name}`}
                          >
                            <span className="block truncate font-semibold">{a.customer_name || 'Customer'}</span>
                            <span className="block truncate opacity-80">{a.product_name}</span>
                          </button>
                        )
                      })}
                  </div>
                </div>
              ))}
            </div>
          </Card>
          {!dayQuery.isLoading && dayAppointments.length === 0 && (
            <p className="mt-3 text-sm text-ink3">No bookings on this day.</p>
          )}
        </>
      )}

      {view === 'list' && (
        <>
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
        </>
      )}

      <Modal open={!!selected} onClose={() => setSelected(null)} title="Booking">
        {selected && row(selected, true)}
      </Modal>

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
