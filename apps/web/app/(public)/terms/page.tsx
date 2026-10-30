import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Terms of service',
  alternates: { canonical: '/terms' },
}

export default function Page() {
  return <ClientPage />
}
