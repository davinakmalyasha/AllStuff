import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Verify email',
  robots: { index: false, follow: false },
}

export default function Page() {
  return <ClientPage />
}
