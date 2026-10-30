"use client"

import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CalendarPlus, MapPin } from 'lucide-react'
import { api, apiBlob, type AppointmentDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Confirm, Modal } from '@/components/ui/Modal'
import { SlotPicker } from '@/components/booking/SlotPicker'
import { PageSpinner } from '@/components/ui/Spinner'
import { toast } from '@/components/ui/Toast'
import { usePageMeta } from '@/lib/meta'

const STATUS_LABEL: Record<AppointmentDTO['status'], string> = {
  pending: 'Awaiting confirmation',
  confirmed: 'Confirmed',
  cancelled: 'Cancelled',
  completed: 'Completed',
  no_show: 'No-show',
}

/** Times are shown in the business's timezone: the booking is a slot in the
 *  shop's day, and a customer travelling or abroad would otherwise read the
 *  wrong hour. */
function timeIn(tz: string | undefined, iso: string): string {
  return new Intl.DateTimeFormat('en-GB', {
    timeZone: tz || 'UTC',
    weekday: 'short',
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(iso))
}

export function MyBookingsPage() {
  usePageMeta('My bookings')
  const qc = useQueryClient()
  const [cancelTarget, setCancelTarget] = useState<AppointmentDTO | null>(null)
  const [rescheduleTarget, setRescheduleTarget] = useState<AppointmentDTO | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['my-bookings'],
    queryFn: () => api<{ appointments: AppointmentDTO[] }>('/me/appointments'),
  })

  const cancel = useMutation({
    mutationFn: (a: AppointmentDTO) => api(`/appointments/${a.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['my-bookings'] })
      toast.success('Booking cancelled')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not cancel the booking.'),
  })

  const reschedule = useMutation({
    mutationFn: ({ a, startsAt }: { a: AppointmentDTO; startsAt: string }) =>
      api<{ appointment: AppointmentDTO }>(`/appointments/${a.id}/reschedule`, {
        method: 'PATCH',
        body: { starts_at: startsAt },
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['my-bookings'] })
      setRescheduleTarget(null)
      toast.success('Booking moved')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not move the booking.'),
  })

  const addToCalendar = async (a: AppointmentDTO) => {
    try {
      const blob = await apiBlob(`/appointments/${a.id}/calendar.ics`)
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = 'booking.ics'
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
    } catch {
      toast.error('Could not download the calendar file.')
    }
  }

  const { upcoming, past } = useMemo(() => {
    const now = Date.now()
    const all = data?.appointments ?? []
    return {
      upcoming: all.filter(
        (a) => new Date(a.ends_at).getTime() >= now && a.status !== 'cancelled',
      ),
      past: all.filter((a) => new Date(a.ends_at).getTime() < now || a.status === 'cancelled'),
    }
  }, [data])

  if (isLoading) return <PageSpinner />

  const card = (a: AppointmentDTO, actionable: boolean) => {
    const canCancel =
      actionable && (a.status === 'pending' || a.status === 'confirmed')
    return (
      <Card key={a.id} className="p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="text-sm font-semibold text-ink">{a.product_name || 'Booking'}</p>
            <p className="mt-0.5 text-xs text-ink3">
              {a.business_slug ? (
                <Link to={`/b/${a.business_slug}`} className="hover:underline">
                  {a.business_name}
                </Link>
              ) : (
                a.business_name
              )}
              {a.location_name ? (
                <span className="ml-1 inline-flex items-center gap-0.5">
                  <MapPin className="h-3 w-3" aria-hidden="true" />
                  {a.location_name}
                </span>
              ) : null}
            </p>
            <p className="mt-2 text-sm text-ink">
              {timeIn(a.business_timezone, a.starts_at)}
              {' – '}
              {/* Only the clock of the end time, not a second date. */}
              {new Intl.DateTimeFormat('en-GB', {
                timeZone: a.business_timezone || 'UTC',
                hour: '2-digit',
                minute: '2-digit',
              }).format(new Date(a.ends_at))}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <span className="rounded-full border border-border px-2 py-0.5 text-[10px] font-medium text-ink2">
              {STATUS_LABEL[a.status]}
            </span>
            <Button
              size="sm"
              variant="ghost"
              aria-label="Add to calendar"
              onClick={() => void addToCalendar(a)}
            >
              <CalendarPlus className="h-3.5 w-3.5" />
            </Button>
            {canCancel && (
              <>
                <Button size="sm" variant="ghost" onClick={() => setRescheduleTarget(a)}>
                  Reschedule
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setCancelTarget(a)}>
                  Cancel
                </Button>
              </>
            )}
          </div>
        </div>
      </Card>
    )
  }

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6">
        <p className="mono-label mb-1">Appointments</p>
        <h1 className="text-2xl font-semibold tracking-tight">My bookings</h1>
        <p className="mt-1 text-sm text-ink2">
          Times are shown in the business's own timezone.
        </p>
      </div>

      {upcoming.length === 0 && past.length === 0 && (
        <Card>
          <p className="text-sm text-ink3">
            Nothing booked yet. Find a service on any business page and pick a time.
          </p>
        </Card>
      )}

      {upcoming.length > 0 && (
        <section aria-labelledby="upcoming-bookings" className="mb-8">
          <h2 id="upcoming-bookings" className="mono-label mb-2">
            Upcoming
          </h2>
          <div className="space-y-3">{upcoming.map((a) => card(a, true))}</div>
        </section>
      )}

      {past.length > 0 && (
        <section aria-labelledby="past-bookings">
          <h2 id="past-bookings" className="mono-label mb-2">
            Past
          </h2>
          <div className="space-y-3">{past.map((a) => card(a, false))}</div>
        </section>
      )}

      <Modal
        open={!!rescheduleTarget}
        onClose={() => setRescheduleTarget(null)}
        title={rescheduleTarget ? `Move ${rescheduleTarget.product_name || 'booking'}` : 'Move booking'}
      >
        {rescheduleTarget && (
          <>
            <p className="mb-3 text-xs text-ink3">
              Currently {timeIn(rescheduleTarget.business_timezone, rescheduleTarget.starts_at)}. Times are shown in the
              business's timezone.
            </p>
            <SlotPicker
              businessId={rescheduleTarget.business_id}
              productId={rescheduleTarget.product_id}
              timezone={rescheduleTarget.business_timezone}
              staffId={rescheduleTarget.staff_id}
              locationId={rescheduleTarget.location_id}
              actionLabel="Move to"
              disabled={reschedule.isPending}
              onPick={(slot) => reschedule.mutate({ a: rescheduleTarget, startsAt: slot.starts_at })}
            />
          </>
        )}
      </Modal>

      <Confirm
        open={!!cancelTarget}
        title="Cancel this booking?"
        message="The business will be told. You can book another time afterwards."
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
