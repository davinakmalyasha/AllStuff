import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Restore account',
  robots: { index: false, follow: false },
}

export default function Page() {
  return <ClientPage />
}
