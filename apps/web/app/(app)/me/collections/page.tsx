"use client"
import { Suspense } from 'react'
import { CollectionsPage } from '@/features/me/CollectionsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="CollectionsPage">
      <Suspense fallback={<PageSpinner />}><CollectionsPage /></Suspense>
    </ErrorBoundary>
  )
}
