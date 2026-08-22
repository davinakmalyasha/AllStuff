import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { api } from '@/lib/api'
import { useCurrency } from '@/stores/currency'

/** Fetches conversion rates once; drives all price displays (PRD D5).
 *  Renders the §5.1.2 staleness annotation when rates are >24h old. */
export function CurrencyProvider() {
  const setRates = useCurrency((s) => s.setRates)
  const stale = useCurrency((s) => s.stale)
  const [dismissed, setDismissed] = useState(false)
  const { data } = useQuery({
    queryKey: ['rates'],
    queryFn: () => api<{ base: string; rates: Record<string, number>; updated_at: string | null }>('/rates'),
    staleTime: 60 * 60_000,
    refetchInterval: 60 * 60_000,
  })
  useEffect(() => {
    if (data) setRates(data.rates, data.updated_at)
  }, [data, setRates])

  if (!stale || dismissed) return null
  return (
    <div role="status" className="fixed bottom-4 left-4 z-40 flex max-w-sm items-start gap-2 rounded-xl border border-border bg-surface px-3 py-2 text-xs text-ink2 shadow-cardHover">
      <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <p>Currency rates are older than 24 hours — converted prices may be out of date.</p>
      <button onClick={() => setDismissed(true)} className="ml-2 shrink-0 font-medium text-ink hover:underline" aria-label="Dismiss">
        Dismiss
      </button>
    </div>
  )
}
