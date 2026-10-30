"use client"
import { Suspense } from 'react'
import { BookingsPage } from '@/features/dashboard/BookingsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="BookingsPage">
      <Suspense fallback={<PageSpinner />}><BookingsPage /></Suspense>
    </ErrorBoundary>
  )
}
