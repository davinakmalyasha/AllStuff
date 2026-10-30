import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Privacy policy',
  alternates: { canonical: '/privacy' },
}

export default function Page() {
  return <ClientPage />
}
