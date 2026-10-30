"use client"
import { Suspense } from 'react'
import { CategoryPage } from '@/features/discover/CategoryPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="CategoryPage">
      <Suspense fallback={<PageSpinner />}><CategoryPage /></Suspense>
    </ErrorBoundary>
  )
}
