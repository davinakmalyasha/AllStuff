"use client"
import { Suspense } from 'react'
import { AdminSettingsPage } from '@/features/admin/AdminSettingsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AdminSettingsPage">
      <Suspense fallback={<PageSpinner />}><AdminSettingsPage /></Suspense>
    </ErrorBoundary>
  )
}
