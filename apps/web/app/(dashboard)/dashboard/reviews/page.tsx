"use client"
import { Suspense } from 'react'
import { DashboardReviewsPage } from '@/features/dashboard/EngagementPages'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="DashboardReviewsPage">
      <Suspense fallback={<PageSpinner />}><DashboardReviewsPage /></Suspense>
    </ErrorBoundary>
  )
}
