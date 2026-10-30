import type { Metadata } from 'next'
import { notFound } from 'next/navigation'
import { apiServer } from '@/lib/api-server'
import { absolute, jsonLdScript } from '@/lib/meta-server'
import ClientPage from './client'

export const revalidate = 300

interface CityDTO {
  slug: string
  name: string
  count: number
  lat: number | null
  lng: number | null
}

async function getCity(slug: string) {
  try {
    return await apiServer<{ city: CityDTO }>(`/cities/${slug}`, { auth: false, revalidate: 300 })
  } catch {
    return null
  }
}

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }): Promise<Metadata> {
  const { slug } = await params
  const data = await getCity(slug)
  const city = data?.city
  if (!city) return { title: 'Cities' }
  const title = `Businesses in ${city.name}`
  return {
    title,
    alternates: { canonical: `/city/${city.slug}` },
    openGraph: { title, url: absolute(`/city/${city.slug}`) },
  }
}

export default async function Page({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params
  const data = await getCity(slug)
  if (!data) notFound()
  const city = data.city
  const jsonLd = {
    '@context': 'https://schema.org',
    '@type': 'Place',
    name: city.name,
    address: { '@type': 'PostalAddress', addressLocality: city.name },
    geo: city.lat && city.lng ? { '@type': 'GeoCoordinates', latitude: city.lat, longitude: city.lng } : undefined,
  }
  return (
    <>
      <script type="application/ld+json" id="bv-jsonld" dangerouslySetInnerHTML={{ __html: jsonLdScript(jsonLd) }} />
      <ClientPage />
    </>
  )
}
