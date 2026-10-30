import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Pricing',
  alternates: { canonical: '/pricing' },
}

export default function Page() {
  return <ClientPage />
}
