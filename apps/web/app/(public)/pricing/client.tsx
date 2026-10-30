"use client"
import { Suspense } from 'react'
import { PricingPage } from '@/features/pages/PricingPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="PricingPage">
      <Suspense fallback={<PageSpinner />}><PricingPage /></Suspense>
    </ErrorBoundary>
  )
}
