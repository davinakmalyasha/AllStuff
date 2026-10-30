"use client"
import { Suspense } from 'react'
import { BusinessHelpPage } from '@/features/help/HelpPages'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="BusinessHelpPage">
      <Suspense fallback={<PageSpinner />}><BusinessHelpPage /></Suspense>
    </ErrorBoundary>
  )
}
