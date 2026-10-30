"use client"
import { Suspense } from 'react'
import { MyBookingsPage } from '@/features/me/MyBookingsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="MyBookingsPage">
      <Suspense fallback={<PageSpinner />}><MyBookingsPage /></Suspense>
    </ErrorBoundary>
  )
}
