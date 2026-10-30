"use client"
import { Suspense } from 'react'
import { BillingPage } from '@/features/dashboard/BillingPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="BillingPage">
      <Suspense fallback={<PageSpinner />}><BillingPage /></Suspense>
    </ErrorBoundary>
  )
}
