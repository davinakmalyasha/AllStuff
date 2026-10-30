"use client"
import { Suspense } from 'react'
import { ExportPage } from '@/features/me/ActivityPages'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="ExportPage">
      <Suspense fallback={<PageSpinner />}><ExportPage /></Suspense>
    </ErrorBoundary>
  )
}
