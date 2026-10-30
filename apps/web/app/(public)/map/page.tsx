import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Map — discover businesses around you',
  alternates: { canonical: '/map' },
}

export default function Page() {
  return <ClientPage />
}
