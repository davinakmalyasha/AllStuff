"use client"
import { Suspense } from 'react'
import { StaffPage } from '@/features/dashboard/StaffPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="StaffPage">
      <Suspense fallback={<PageSpinner />}><StaffPage /></Suspense>
    </ErrorBoundary>
  )
}
