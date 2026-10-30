"use client"
import { Suspense } from 'react'
import { LeaderboardsPage } from '@/features/discover/LeaderboardsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="LeaderboardsPage">
      <Suspense fallback={<PageSpinner />}><LeaderboardsPage /></Suspense>
    </ErrorBoundary>
  )
}
