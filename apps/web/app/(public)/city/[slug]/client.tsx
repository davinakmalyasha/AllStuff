"use client"
import { Suspense } from 'react'
import { CityPage } from '@/features/discover/CityPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="CityPage">
      <Suspense fallback={<PageSpinner />}><CityPage /></Suspense>
    </ErrorBoundary>
  )
}
