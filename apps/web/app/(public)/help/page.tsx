import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Help center',
  alternates: { canonical: '/help' },
}

export default function Page() {
  return <ClientPage />
}
