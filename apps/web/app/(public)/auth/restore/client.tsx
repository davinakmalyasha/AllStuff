"use client"
import { Suspense } from 'react'
import { RestorePage } from '@/features/auth/RestorePage'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'
import { GuestOnly } from '@/components/guards'

export default function ClientPage() {
  return (
    <ErrorBoundary label="RestorePage">
      <Suspense fallback={<PageSpinner />}><GuestOnly><RestorePage /></GuestOnly></Suspense>
    </ErrorBoundary>
  )
}
