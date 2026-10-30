"use client"
import { Suspense } from 'react'
import { TwoFactorPage } from '@/features/auth/TwoFactorPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'
import { GuestOnly } from '@/components/guards'

export default function ClientPage() {
  return (
    <ErrorBoundary label="TwoFactorPage">
      <Suspense fallback={<PageSpinner />}><GuestOnly><TwoFactorPage /></GuestOnly></Suspense>
    </ErrorBoundary>
  )
}
