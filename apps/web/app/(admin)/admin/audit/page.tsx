"use client"
import { Suspense } from 'react'
import { AdminAuditPage } from '@/features/admin/AdminAuditPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AdminAuditPage">
      <Suspense fallback={<PageSpinner />}><AdminAuditPage /></Suspense>
    </ErrorBoundary>
  )
}
