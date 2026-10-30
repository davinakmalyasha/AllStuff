"use client"
import { Suspense } from 'react'
import { BusinessPage } from '@/features/business/BusinessPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="BusinessPage">
      <Suspense fallback={<PageSpinner />}><BusinessPage /></Suspense>
    </ErrorBoundary>
  )
}
