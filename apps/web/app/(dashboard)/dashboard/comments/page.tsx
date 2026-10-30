"use client"
import { Suspense } from 'react'
import { DashboardCommentsPage } from '@/features/dashboard/EngagementPages'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="DashboardCommentsPage">
      <Suspense fallback={<PageSpinner />}><DashboardCommentsPage /></Suspense>
    </ErrorBoundary>
  )
}
