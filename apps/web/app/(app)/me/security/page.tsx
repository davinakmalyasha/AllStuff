"use client"
import { Suspense } from 'react'
import { SecurityPage } from '@/features/me/SecurityPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="SecurityPage">
      <Suspense fallback={<PageSpinner />}><SecurityPage /></Suspense>
    </ErrorBoundary>
  )
}
