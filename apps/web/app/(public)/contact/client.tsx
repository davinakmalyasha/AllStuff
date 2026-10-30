"use client"
import { Suspense } from 'react'
import { ContactPage } from '@/features/help/ContactPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="ContactPage">
      <Suspense fallback={<PageSpinner />}><ContactPage /></Suspense>
    </ErrorBoundary>
  )
}
