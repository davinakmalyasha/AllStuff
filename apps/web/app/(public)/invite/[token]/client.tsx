"use client"
import { Suspense } from 'react'
import { InviteAcceptPage } from '@/features/pages/InviteAcceptPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="InviteAcceptPage">
      <Suspense fallback={<PageSpinner />}><InviteAcceptPage /></Suspense>
    </ErrorBoundary>
  )
}
