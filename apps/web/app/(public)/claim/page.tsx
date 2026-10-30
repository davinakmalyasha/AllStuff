import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Claim your business',
  alternates: { canonical: '/claim' },
}

export default function Page() {
  return <ClientPage />
}
