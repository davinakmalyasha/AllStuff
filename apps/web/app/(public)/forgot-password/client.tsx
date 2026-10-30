"use client"
import { Suspense } from 'react'
import { ForgotPasswordPage } from '@/features/auth/ForgotPasswordPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="ForgotPasswordPage">
      <Suspense fallback={<PageSpinner />}><ForgotPasswordPage /></Suspense>
    </ErrorBoundary>
  )
}
