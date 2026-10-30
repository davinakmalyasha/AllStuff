"use client"
import { Suspense } from 'react'
import { PrivacyPage } from '@/features/pages/LegalPages'
import { PageSpinner } from '@/components/ui/Spinner'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'

export default function ClientPage() {
  return (
    <ErrorBoundary label="PrivacyPage">
      <Suspense fallback={<PageSpinner />}><PrivacyPage /></Suspense>
    </ErrorBoundary>
  )
}
