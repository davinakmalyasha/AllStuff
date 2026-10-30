"use client"
import { Suspense } from 'react'
import { MyReviewsPage } from '@/features/me/ActivityPages'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="MyReviewsPage">
      <Suspense fallback={<PageSpinner />}><MyReviewsPage /></Suspense>
    </ErrorBoundary>
  )
}
