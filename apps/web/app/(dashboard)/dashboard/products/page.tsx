"use client"
import { Suspense } from 'react'
import { ProductsPage } from '@/features/dashboard/ProductsPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function Page() {
  return (
    <ErrorBoundary label="ProductsPage">
      <Suspense fallback={<PageSpinner />}><ProductsPage /></Suspense>
    </ErrorBoundary>
  )
}
