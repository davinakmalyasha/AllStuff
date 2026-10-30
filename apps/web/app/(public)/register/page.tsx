import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Create account',
  robots: { index: false, follow: false },
}

export default function Page() {
  return <ClientPage />
}
