"use client"
import { Suspense } from 'react'
import { CategoriesPage } from '@/features/pages/StaticPages'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="CategoriesPage">
      <Suspense fallback={<PageSpinner />}><CategoriesPage /></Suspense>
    </ErrorBoundary>
  )
}
