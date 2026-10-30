import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Owner guide',
  alternates: { canonical: '/help/business' },
}

export default function Page() {
  return <ClientPage />
}
