import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Leaderboards',
  alternates: { canonical: '/leaderboards' },
}

export default function Page() {
  return <ClientPage />
}
