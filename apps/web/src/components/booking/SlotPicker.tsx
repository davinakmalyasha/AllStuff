"use client"

import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type SlotDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'

/** How many days ahead the picker offers. Booking further out is rare and the
 *  strip has to stay usable on a phone. */
const DAYS_AHEAD = 14

function isoDate(d: Date): string {
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${d.getFullYear()}-${m}-${day}`
}

interface SlotPickerProps {
  businessId: string
  productId: string
  /** The business's timezone: a slot is an hour in the shop's day, not the viewer's. */
  timezone?: string
  /** Narrows availability to the person and place the booking already belongs to. */
  staffId?: string
  locationId?: string
  onPick: (slot: SlotDTO) => void
  disabled?: boolean
  /** Label for the slot button, so screen readers hear the action, not just a time. */
  actionLabel?: string
}

/**
 * Day strip plus free times for one service. Extracted from BookingPanel so the
 * reschedule dialog offers exactly the same slots, in the same timezone, under
 * the same availability rules - two pickers would drift.
 */
export function SlotPicker({
  businessId,
  productId,
  timezone,
  staffId,
  locationId,
  onPick,
  disabled = false,
  actionLabel = 'Book',
}: SlotPickerProps) {
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

  const timeFmt = useMemo(
    () =>
      new Intl.DateTimeFormat('en-GB', {
        timeZone: timezone || 'UTC',
        hour: '2-digit',
        minute: '2-digit',
      }),
    [timezone],
  )
  const dateFmt = useMemo(
    () =>
      new Intl.DateTimeFormat('en-GB', {
        timeZone: timezone || 'UTC',
        weekday: 'short',
        day: 'numeric',
        month: 'short',
      }),
    [timezone],
  )

  const query = new URLSearchParams({ product_id: productId, date })
  if (staffId) query.set('staff_id', staffId)
  if (locationId) query.set('location_id', locationId)

  const availability = useQuery({
    queryKey: ['availability', businessId, productId, date, staffId ?? '', locationId ?? ''],
    queryFn: () => api<{ slots: SlotDTO[] }>(`/businesses/${businessId}/availability?${query.toString()}`),
  })

  const slots = availability.data?.slots ?? []

  return (
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
              aria-label={`${actionLabel} at ${timeFmt.format(new Date(s.starts_at))}`}
              disabled={disabled}
              onClick={() => onPick(s)}
            >
              {timeFmt.format(new Date(s.starts_at))}
            </Button>
          ))}
        </div>
      )}
    </>
  )
}
