"use client"
import { Suspense } from 'react'
import { AdminCurationPage } from '@/features/admin/AdminCurationPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AdminCurationPage">
      <Suspense fallback={<PageSpinner />}><AdminCurationPage /></Suspense>
    </ErrorBoundary>
  )
}
