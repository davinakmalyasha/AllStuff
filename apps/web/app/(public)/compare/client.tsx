"use client"
import { Suspense } from 'react'
import { ComparePage } from '@/features/compare/ComparePage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="ComparePage">
      <Suspense fallback={<PageSpinner />}><ComparePage /></Suspense>
    </ErrorBoundary>
  )
}
