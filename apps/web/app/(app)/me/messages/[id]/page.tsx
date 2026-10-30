"use client"
import { Suspense } from 'react'
import { ThreadPage } from '@/features/chat/ThreadPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="ThreadPage">
      <Suspense fallback={<PageSpinner />}><ThreadPage /></Suspense>
    </ErrorBoundary>
  )
}
