"use client"

import { useEffect } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { ThemeProvider } from '@/theme/ThemeProvider'
import { CurrencyProvider } from '@/components/CurrencyProvider'
import { ToastStack } from '@/components/ui/Toast'
import { LiveIsland } from '@/components/LiveIsland'
import i18n, { resolvePreferredLng } from '@/lib/i18n'

export function Providers({ children }: { children: React.ReactNode }) {
  // Post-mount locale adoption (see resolvePreferredLng): first paint stays
  // English to match the SSR HTML; the saved/navigator language applies as a
  // normal update right after.
  useEffect(() => {
    const preferred = resolvePreferredLng()
    if (preferred !== i18n.language) void i18n.changeLanguage(preferred)
  }, [])
  return (
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <CurrencyProvider />
        <ToastStack />
        <LiveIsland />
        {children}
      </QueryClientProvider>
    </ThemeProvider>
  )
}
