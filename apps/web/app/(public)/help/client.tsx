"use client"
import { Suspense } from 'react'
import { HelpPage } from '@/features/help/HelpPages'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="HelpPage">
      <Suspense fallback={<PageSpinner />}><HelpPage /></Suspense>
    </ErrorBoundary>
  )
}
