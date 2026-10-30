"use client"
import { Suspense } from 'react'
import { InboxPage } from '@/features/chat/InboxPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="InboxPage">
      <Suspense fallback={<PageSpinner />}><InboxPage /></Suspense>
    </ErrorBoundary>
  )
}
