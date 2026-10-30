"use client"

import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { BellRing } from 'lucide-react'
import { api, type WaitlistDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { toast } from '@/components/ui/Toast'

/**
 * Offered in place of "No times left" for a full day. Joining is idempotent on
 * the server; the local flag is only the immediate confirmation - the entry
 * also appears under My bookings, where it can be left.
 */
export function WaitlistButton({ businessId, productId, day }: { businessId: string; productId: string; day: string }) {
  const [joined, setJoined] = useState(false)

  const join = useMutation({
    mutationFn: () =>
      api<{ waitlist: WaitlistDTO }>(`/businesses/${businessId}/waitlist`, {
        method: 'POST',
        body: { product_id: productId, day },
      }),
    onSuccess: () => {
      setJoined(true)
      toast.success('You will be told if a time opens up')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not join the waitlist.'),
  })

  if (joined) {
    return (
      <p className="flex items-center gap-1.5 py-3 text-sm text-ink2">
        <BellRing className="h-3.5 w-3.5" aria-hidden="true" />
        On the waitlist for this day. Leave it any time from My bookings.
      </p>
    )
  }

  return (
    <div className="flex flex-wrap items-center gap-2 py-3">
      <p className="text-sm text-ink3">No times left on this day.</p>
      <Button size="sm" variant="secondary" disabled={join.isPending} onClick={() => join.mutate()}>
        <BellRing className="h-3.5 w-3.5" aria-hidden="true" />
        Tell me if a time opens
      </Button>
    </div>
  )
}
