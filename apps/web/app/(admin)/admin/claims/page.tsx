"use client"
import { Suspense } from 'react'
import { AdminClaimsPage } from '@/features/admin/AdminClaimsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AdminClaimsPage">
      <Suspense fallback={<PageSpinner />}><AdminClaimsPage /></Suspense>
    </ErrorBoundary>
  )
}
