"use client"
import { Suspense } from 'react'
import { FollowingFeedPage } from '@/features/me/FollowingFeedPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="FollowingFeedPage">
      <Suspense fallback={<PageSpinner />}><FollowingFeedPage /></Suspense>
    </ErrorBoundary>
  )
}
