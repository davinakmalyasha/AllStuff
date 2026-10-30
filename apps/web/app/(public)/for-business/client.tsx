"use client"
import { Suspense } from 'react'
import { ForBusinessPage } from '@/features/pages/StaticPages'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="ForBusinessPage">
      <Suspense fallback={<PageSpinner />}><ForBusinessPage /></Suspense>
    </ErrorBoundary>
  )
}
