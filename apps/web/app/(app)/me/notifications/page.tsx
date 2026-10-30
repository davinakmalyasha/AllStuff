"use client"
import { Suspense } from 'react'
import { NotificationsPage } from '@/features/me/NotificationsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="NotificationsPage">
      <Suspense fallback={<PageSpinner />}><NotificationsPage /></Suspense>
    </ErrorBoundary>
  )
}
