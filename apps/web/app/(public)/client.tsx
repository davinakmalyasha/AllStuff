"use client"
import { Suspense } from 'react'
import { LandingPage } from '@/features/landing/LandingPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="LandingPage">
      <Suspense fallback={<PageSpinner />}><LandingPage /></Suspense>
    </ErrorBoundary>
  )
}
