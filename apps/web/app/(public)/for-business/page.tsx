import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'For business',
  alternates: { canonical: '/for-business' },
}

export default function Page() {
  return <ClientPage />
}
