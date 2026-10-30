"use client"
import { Suspense } from 'react'
import { TermsPage } from '@/features/pages/LegalPages'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="TermsPage">
      <Suspense fallback={<PageSpinner />}><TermsPage /></Suspense>
    </ErrorBoundary>
  )
}
