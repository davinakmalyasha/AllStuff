"use client"
import { Suspense } from 'react'
import { AdminModerationPage } from '@/features/admin/AdminModerationPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AdminModerationPage">
      <Suspense fallback={<PageSpinner />}><AdminModerationPage /></Suspense>
    </ErrorBoundary>
  )
}
