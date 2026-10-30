import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Categories',
  alternates: { canonical: '/categories' },
}

export default function Page() {
  return <ClientPage />
}
