import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery } from '@tanstack/react-query'
import { CalendarDays } from 'lucide-react'
import {
  api,
  type AppointmentDTO,
  type BusinessDTO,
  type ProductDTO,
  type SlotDTO,
} from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { toast } from '@/components/ui/Toast'
import { useAuthState } from '@/stores/auth'

/** How many days ahead the picker offers. Booking further out is rare and the
 *  strip has to stay usable on a phone. */
const DAYS_AHEAD = 14

function isoDate(d: Date): string {
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${d.getFullYear()}-${m}-${day}`
}

/**
 * The customer's half of a booking: pick a day, pick a free time, confirm.
 *
 * Times are rendered in the BUSINESS's timezone, not the browser's. A customer
 * browsing from another country is booking a slot in the shop's day, and showing
 * it at their own local time would make them arrive at the wrong hour.
 */
export function BookingPanel({ business, product }: { business: BusinessDTO; product: ProductDTO }) {
  const { user } = useAuthState((s) => ({ user: s.user }))
  const days = useMemo(
    () =>
      Array.from({ length: DAYS_AHEAD }, (_, i) => {
        const d = new Date()
        d.setDate(d.getDate() + i)
        return d
      }),
    [],
  )
  const [date, setDate] = useState(() => isoDate(days[0]))
  const [booked, setBooked] = useState<AppointmentDTO | null>(null)

  const timeFmt = useMemo(
    () =>
      new Intl.DateTimeFormat('en-GB', {
        timeZone: business.timezone || 'UTC',
        hour: '2-digit',
        minute: '2-digit',
      }),
    [business.timezone],
  )
  const dateFmt = useMemo(
    () =>
      new Intl.DateTimeFormat('en-GB', {
        timeZone: business.timezone || 'UTC',
        weekday: 'short',
        day: 'numeric',
        month: 'short',
      }),
    [business.timezone],
  )

  const availability = useQuery({
    queryKey: ['availability', business.id, product.id, date],
    queryFn: () =>
      api<{ slots: SlotDTO[] }>(
        `/businesses/${business.id}/availability?product_id=${product.id}&date=${date}`,
      ),
    enabled: !!user,
  })

  const book = useMutation({
    mutationFn: (slot: SlotDTO) =>
      api<{ appointment: AppointmentDTO }>(`/businesses/${business.id}/appointments`, {
        method: 'POST',
        body: { product_id: product.id, starts_at: slot.starts_at },
      }),
    onSuccess: (r) => {
      setBooked(r.appointment)
      toast.success('Booked')
      void availability.refetch()
    },
    onError: (e) => {
      // The most likely failure is a slot taken between loading this page and
      // pressing the button. Refreshing is the useful response; leaving the same
      // dead slot on screen is not.
      toast.error((e as Error).message || 'Could not book that time.')
      void availability.refetch()
    },
  })

  const slots = availability.data?.slots ?? []

  return (
    <Card className="mt-4">
      <div className="mb-3 flex items-center gap-2">
        <CalendarDays className="h-4 w-4 text-accent-ink" aria-hidden="true" />
        <h3 className="text-sm font-semibold text-ink">Book {product.name}</h3>
        {product.duration_minutes ? (
          <span className="text-xs text-ink3">{product.duration_minutes} min</span>
        ) : null}
      </div>

      {!user && (
        <div className="flex flex-wrap items-center gap-3">
          <p className="text-sm text-ink2">Sign in to see available times.</p>
          <Link to="/login">
            <Button size="sm">Sign in</Button>
          </Link>
        </div>
      )}

      {user && booked && (
        <div className="rounded-lg border border-border bg-surface2 p-3">
          <p className="text-sm font-medium text-ink">
            Booked for {dateFmt.format(new Date(booked.starts_at))} at{' '}
            {timeFmt.format(new Date(booked.starts_at))}
          </p>
          <p className="mt-1 text-xs text-ink3">
            You can cancel, or add it to your calendar, from{' '}
            <Link to="/me/bookings" className="underline">
              My bookings
            </Link>
            .
          </p>
          <Button size="sm" variant="secondary" className="mt-2" onClick={() => setBooked(null)}>
            Book another time
          </Button>
        </div>
      )}

      {user && !booked && (
        <>
          <div className="flex gap-1.5 overflow-x-auto pb-2" role="group" aria-label="Choose a day">
            {days.map((d) => {
              const iso = isoDate(d)
              const selected = iso === date
              return (
                <button
                  key={iso}
                  type="button"
                  onClick={() => setDate(iso)}
                  aria-pressed={selected}
                  aria-label={dateFmt.format(d)}
                  className={`shrink-0 rounded-lg border px-2.5 py-1.5 text-center text-xs transition-colors ${
                    selected
                      ? 'border-accent bg-accent text-white'
                      : 'border-border bg-surface text-ink2 hover:border-accent'
                  }`}
                >
                  <span className="block font-semibold">{d.getDate()}</span>
                  <span className="block text-[10px] opacity-80">
                    {d.toLocaleDateString('en-GB', { weekday: 'short' })}
                  </span>
                </button>
              )
            })}
          </div>

          {availability.isLoading && <p className="py-3 text-sm text-ink3">Loading times…</p>}
          {availability.isError && (
            <p className="py-3 text-sm text-ink3">Could not load availability. Try another day.</p>
          )}
          {!availability.isLoading && !availability.isError && slots.length === 0 && (
            <p className="py-3 text-sm text-ink3">No times left on this day.</p>
          )}
          {slots.length > 0 && (
            <div className="mt-1 flex flex-wrap gap-2" role="group" aria-label="Choose a time">
              {slots.map((s) => (
                <Button
                  key={s.starts_at}
                  size="sm"
                  variant="secondary"
                  aria-label={`Book at ${timeFmt.format(new Date(s.starts_at))}`}
                  disabled={book.isPending}
                  onClick={() => book.mutate(s)}
                >
                  {timeFmt.format(new Date(s.starts_at))}
                </Button>
              ))}
            </div>
          )}
        </>
      )}
    </Card>
  )
}
