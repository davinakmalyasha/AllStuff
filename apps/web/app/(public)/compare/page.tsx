import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Compare businesses',
  alternates: { canonical: '/compare' },
}

export default function Page() {
  return <ClientPage />
}
