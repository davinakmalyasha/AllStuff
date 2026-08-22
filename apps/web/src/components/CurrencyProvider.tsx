import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useCurrency } from '@/stores/currency'

/** Fetches conversion rates once; drives all price displays (PRD D5). */
export function CurrencyProvider() {
  const setRates = useCurrency((s) => s.setRates)
  const { data } = useQuery({
    queryKey: ['rates'],
    queryFn: () => api<{ base: string; rates: Record<string, number>; updated_at: string | null }>('/rates'),
    staleTime: 60 * 60_000,
    refetchInterval: 60 * 60_000,
  })
  useEffect(() => {
    if (data) setRates(data.rates, data.updated_at)
  }, [data, setRates])
  return null
}
