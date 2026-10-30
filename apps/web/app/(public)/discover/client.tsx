"use client"
import { Suspense } from 'react'
import { DiscoverPage } from '@/features/discover/DiscoverPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="DiscoverPage">
      <Suspense fallback={<PageSpinner />}><DiscoverPage /></Suspense>
    </ErrorBoundary>
  )
}
