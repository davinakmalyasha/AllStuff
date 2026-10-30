"use client"
import { Suspense } from 'react'
import { AdminAppealsPage } from '@/features/admin/AdminAppealsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AdminAppealsPage">
      <Suspense fallback={<PageSpinner />}><AdminAppealsPage /></Suspense>
    </ErrorBoundary>
  )
}
