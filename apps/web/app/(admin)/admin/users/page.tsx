"use client"
import { Suspense } from 'react'
import { AdminUsersPage } from '@/features/admin/AdminUsersPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AdminUsersPage">
      <Suspense fallback={<PageSpinner />}><AdminUsersPage /></Suspense>
    </ErrorBoundary>
  )
}
