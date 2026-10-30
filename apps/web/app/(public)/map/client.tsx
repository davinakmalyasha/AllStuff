"use client"
import { Suspense } from 'react'
import { MapPage } from '@/features/map/MapPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="MapPage">
      <Suspense fallback={<PageSpinner />}><MapPage /></Suspense>
    </ErrorBoundary>
  )
}
