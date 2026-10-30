"use client"
import { Suspense } from 'react'
import { ResetPasswordPage } from '@/features/auth/ResetPasswordPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="ResetPasswordPage">
      <Suspense fallback={<PageSpinner />}><ResetPasswordPage /></Suspense>
    </ErrorBoundary>
  )
}
