"use client"
import { Suspense } from 'react'
import { UserProfilePage } from '@/features/me/UserProfilePage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="UserProfilePage">
      <Suspense fallback={<PageSpinner />}><UserProfilePage /></Suspense>
    </ErrorBoundary>
  )
}
