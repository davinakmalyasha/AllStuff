import type { Metadata } from 'next'
import ClientPage from './client'

export const metadata: Metadata = {
  title: 'Discover businesses',
  description: 'Search every business in one place — filter by category, city, price, rating, and open hours.',
  alternates: { canonical: '/discover' },
}

export default function Page() {
  return <ClientPage />
}
