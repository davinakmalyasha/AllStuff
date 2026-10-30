"use client"
import { Suspense } from 'react'
import { RegisterPage } from '@/features/auth/RegisterPage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'
import { GuestOnly } from '@/components/guards'

export default function ClientPage() {
  return (
    <ErrorBoundary label="RegisterPage">
      <Suspense fallback={<PageSpinner />}><GuestOnly><RegisterPage /></GuestOnly></Suspense>
    </ErrorBoundary>
  )
}
