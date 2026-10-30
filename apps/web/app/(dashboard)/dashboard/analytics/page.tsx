"use client"
import { Suspense } from 'react'
import { AnalyticsPage } from '@/features/dashboard/AnalyticsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AnalyticsPage">
      <Suspense fallback={<PageSpinner />}><AnalyticsPage /></Suspense>
    </ErrorBoundary>
  )
}
