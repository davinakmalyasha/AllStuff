"use client"
import { Suspense } from 'react'
import { BusinessWizardPage } from '@/features/dashboard/BusinessWizardPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="BusinessWizardPage">
      <Suspense fallback={<PageSpinner />}><BusinessWizardPage /></Suspense>
    </ErrorBoundary>
  )
}
