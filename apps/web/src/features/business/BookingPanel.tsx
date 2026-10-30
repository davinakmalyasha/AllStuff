"use client"

import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
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
import { SlotPicker } from '@/components/booking/SlotPicker'
import { useAuthState } from '@/stores/auth'

/**
 * The customer's half of a booking: pick a day, pick a free time, confirm.
 *
 * Times are rendered in the BUSINESS's timezone, not the browser's. A customer
 * browsing from another country is booking a slot in the shop's day, and showing
 * it at their own local time would make them arrive at the wrong hour.
 */
export function BookingPanel({ business, product }: { business: BusinessDTO; product: ProductDTO }) {
  const { user } = useAuthState((s) => ({ user: s.user }))
  const [booked, setBooked] = useState<AppointmentDTO | null>(null)

  const timeFmt = new Intl.DateTimeFormat('en-GB', {
    timeZone: business.timezone || 'UTC',
    weekday: 'short',
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
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
    },
    onError: (e) => {
      // The most likely failure is a slot taken between loading this page and
      // pressing the button. The picker refetches on remount; leaving the same
      // dead slot on screen is not useful.
      toast.error((e as Error).message || 'Could not book that time.')
    },
  })

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
            Booked for {timeFmt.format(new Date(booked.starts_at))}
          </p>
          <p className="mt-1 text-xs text-ink3">
            You can reschedule or cancel, or add it to your calendar, from{' '}
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
        <SlotPicker
          businessId={business.id}
          productId={product.id}
          timezone={business.timezone}
          onPick={(slot) => book.mutate(slot)}
          disabled={book.isPending}
        />
      )}
    </Card>
  )
}
