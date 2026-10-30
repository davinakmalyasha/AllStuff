import type { Metadata } from 'next'
import { notFound } from 'next/navigation'
import { apiServer } from '@/lib/api-server'
import { absolute, businessJsonLd, jsonLdScript } from '@/lib/meta-server'
import type { BusinessDTO } from '@/lib/api'
import ClientPage from './client'

export const revalidate = 300

async function getBusiness(slug: string) {
  try {
    return await apiServer<{ business: BusinessDTO }>(`/b/${slug}`, { auth: false, revalidate: 300 })
  } catch {
    return null
  }
}

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }): Promise<Metadata> {
  const { slug } = await params
  const data = await getBusiness(slug)
  const b = data?.business
  if (!b) return { title: 'Business' }
  const desc = (b.tagline ?? b.description ?? '').slice(0, 160)
  return {
    title: `${b.name} — ${b.category_name ?? 'Business'}`,
    description: desc || undefined,
    alternates: { canonical: `/b/${b.slug}` },
    openGraph: {
      title: `${b.name} — ${b.category_name ?? 'Business'}`,
      description: desc || undefined,
      url: absolute(`/b/${b.slug}`),
      images: [{ url: absolute(`/og/b/${b.slug}`), width: 1200, height: 630 }],
    },
  }
}

export default async function Page({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params
  const data = await getBusiness(slug)
  if (!data) notFound()
  const b = data.business
  return (
    <>
      <script type="application/ld+json" id="bv-jsonld" dangerouslySetInnerHTML={{ __html: jsonLdScript(businessJsonLd(b)) }} />
      <ClientPage />
    </>
  )
}
