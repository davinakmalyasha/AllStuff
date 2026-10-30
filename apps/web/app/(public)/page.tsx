import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  alternates: { canonical: '/' },
}

export default function Page() {
  return <ClientPage />
}
