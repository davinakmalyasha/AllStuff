"use client"
import { Suspense } from 'react'
import { AdminKPIPage } from '@/features/admin/AdminKPIPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AdminKPIPage">
      <Suspense fallback={<PageSpinner />}><AdminKPIPage /></Suspense>
    </ErrorBoundary>
  )
}
