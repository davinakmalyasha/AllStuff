"use client"
import { Suspense } from 'react'
import { ClaimPage } from '@/features/pages/ClaimPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="ClaimPage">
      <Suspense fallback={<PageSpinner />}><ClaimPage /></Suspense>
    </ErrorBoundary>
  )
}
