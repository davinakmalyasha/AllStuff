"use client"
import { Suspense } from 'react'
import { PublicCollectionPage } from '@/features/me/PublicCollectionPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="PublicCollectionPage">
      <Suspense fallback={<PageSpinner />}><PublicCollectionPage /></Suspense>
    </ErrorBoundary>
  )
}
