"use client"
import { Suspense } from 'react'
import { VerificationSettingsPage } from '@/features/dashboard/VerificationSettingsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="VerificationSettingsPage">
      <Suspense fallback={<PageSpinner />}><VerificationSettingsPage /></Suspense>
    </ErrorBoundary>
  )
}
