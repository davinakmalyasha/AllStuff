"use client"
import { Suspense } from 'react'
import { VerifyEmailPage } from '@/features/auth/VerifyEmailPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="VerifyEmailPage">
      <Suspense fallback={<PageSpinner />}><VerifyEmailPage /></Suspense>
    </ErrorBoundary>
  )
}
