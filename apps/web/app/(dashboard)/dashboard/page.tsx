"use client"
import { Suspense } from 'react'
import { DashboardIndex } from '@/features/dashboard/DashboardIndex'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="DashboardIndex">
      <Suspense fallback={<PageSpinner />}>
        <DashboardIndex />
      </Suspense>
    </ErrorBoundary>
  )
}
