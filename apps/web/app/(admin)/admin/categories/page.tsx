"use client"
import { Suspense } from 'react'
import { AdminCategoriesPage } from '@/features/admin/AdminCategoriesPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="AdminCategoriesPage">
      <Suspense fallback={<PageSpinner />}><AdminCategoriesPage /></Suspense>
    </ErrorBoundary>
  )
}
