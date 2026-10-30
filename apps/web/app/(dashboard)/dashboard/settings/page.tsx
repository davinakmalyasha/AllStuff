"use client"
import { Suspense } from 'react'
import { SettingsPage } from '@/features/dashboard/SettingsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="SettingsPage">
      <Suspense fallback={<PageSpinner />}><SettingsPage /></Suspense>
    </ErrorBoundary>
  )
}
