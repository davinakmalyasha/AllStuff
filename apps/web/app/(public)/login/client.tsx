"use client"
import { Suspense } from 'react'
import { LoginPage } from '@/features/auth/LoginPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'
import { GuestOnly } from '@/components/guards'

export default function ClientPage() {
  return (
    <ErrorBoundary label="LoginPage">
      <Suspense fallback={<PageSpinner />}><GuestOnly><LoginPage /></GuestOnly></Suspense>
    </ErrorBoundary>
  )
}
