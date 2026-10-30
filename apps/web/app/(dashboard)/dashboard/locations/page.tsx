"use client"
import { Suspense } from 'react'
import { LocationsPage } from '@/features/dashboard/LocationsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="LocationsPage">
      <Suspense fallback={<PageSpinner />}><LocationsPage /></Suspense>
    </ErrorBoundary>
  )
}
