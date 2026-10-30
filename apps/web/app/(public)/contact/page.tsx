import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Contact us',
  alternates: { canonical: '/contact' },
}

export default function Page() {
  return <ClientPage />
}
