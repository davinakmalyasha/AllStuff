"use client"
import { Suspense } from 'react'
import { StorefrontBuilderPage } from '@/features/dashboard/StorefrontBuilderPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="StorefrontBuilderPage">
      <Suspense fallback={<PageSpinner />}><StorefrontBuilderPage /></Suspense>
    </ErrorBoundary>
  )
}
