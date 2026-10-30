"use client"
import { Suspense } from 'react'
import { MePage } from '@/features/me/MePage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="MePage">
      <Suspense fallback={<PageSpinner />}><MePage /></Suspense>
    </ErrorBoundary>
  )
}
