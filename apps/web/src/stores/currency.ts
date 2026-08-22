import { create } from 'zustand'

interface CurrencyState {
  rates: Record<string, number>
  updatedAt: string | null
  stale: boolean
  display: string
  setRates: (rates: Record<string, number>, updatedAt: string | null) => void
  setDisplay: (code: string) => void
}

const DEFAULT_CURRENCY = 'USD'

function detect(): string {
  try {
    const locale = navigator.language || 'en-US'
    const region = new Intl.Locale(locale).region ?? 'US'
    const map: Record<string, string> = {
      US: 'USD', GB: 'GBP', EU: 'EUR', ID: 'IDR', SG: 'SGD', MY: 'MYR', TH: 'THB',
      VN: 'VND', PH: 'PHP', JP: 'JPY', KR: 'KRW', AU: 'AUD', CA: 'CAD', IN: 'INR',
      NL: 'EUR', DE: 'EUR', FR: 'EUR', ES: 'EUR', IT: 'EUR', CN: 'CNY', HK: 'HKD',
      TW: 'TWD', BR: 'BRL', MX: 'MXN', TR: 'TRY', SA: 'SAR', AE: 'AED', NZ: 'NZD',
    }
    return map[region] ?? DEFAULT_CURRENCY
  } catch {
    return DEFAULT_CURRENCY
  }
}

export const useCurrency = create<CurrencyState>((set) => ({
  rates: { USD: 1 },
  updatedAt: null,
  stale: false,
  display: (() => {
    try {
      return localStorage.getItem('bv.currency') ?? detect()
    } catch {
      return detect()
    }
  })(),
  setRates: (rates, updatedAt) =>
    set({
      rates,
      updatedAt,
      stale: updatedAt ? Date.now() - new Date(updatedAt).getTime() > 24 * 3600_000 : false,
    }),
  setDisplay: (code) => {
    try {
      localStorage.setItem('bv.currency', code)
    } catch {
      /* noop */
    }
    set({ display: code })
  },
}))

export function formatMoney(amount: number, currency: string, display: string, rates: Record<string, number>): string {
  if (!rates[currency] || !rates[display]) {
    return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(amount)
  }
  const converted = (amount / rates[currency]) * rates[display]
  return new Intl.NumberFormat(undefined, { style: 'currency', currency: display, maximumFractionDigits: 2 }).format(converted)
}

export const CURRENCIES = ['USD', 'EUR', 'GBP', 'IDR', 'SGD', 'MYR', 'THB', 'VND', 'PHP', 'JPY', 'KRW', 'AUD', 'CAD', 'INR', 'CNY', 'HKD', 'BRL']
