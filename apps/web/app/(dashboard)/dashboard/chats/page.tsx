"use client"
import { Suspense } from 'react'
import { DashboardChatsPage } from '@/features/chat/DashboardChatsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="DashboardChatsPage">
      <Suspense fallback={<PageSpinner />}><DashboardChatsPage /></Suspense>
    </ErrorBoundary>
  )
}
