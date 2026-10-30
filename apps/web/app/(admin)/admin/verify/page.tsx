"use client"
import { Suspense } from 'react'
import { AdminVerifyPage } from '@/features/admin/AdminVerifyPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AdminVerifyPage">
      <Suspense fallback={<PageSpinner />}><AdminVerifyPage /></Suspense>
    </ErrorBoundary>
  )
}
