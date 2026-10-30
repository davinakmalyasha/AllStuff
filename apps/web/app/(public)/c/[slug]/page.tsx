import type { Metadata } from 'next'
import { notFound } from 'next/navigation'
import { apiServer } from '@/lib/api-server'
import { absolute, jsonLdScript } from '@/lib/meta-server'
import type { CategoryDTO } from '@/lib/api'
import ClientPage from './client'

export const revalidate = 300

async function getCategory(slug: string) {
  try {
    return await apiServer<{ category: CategoryDTO }>(`/categories/${slug}`, { auth: false, revalidate: 300 })
  } catch {
    return null
  }
}

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }): Promise<Metadata> {
  const { slug } = await params
  const data = await getCategory(slug)
  const c = data?.category
  if (!c) return { title: 'Category' }
  return {
    title: `${c.name} — Businesses`,
    description: c.description?.slice(0, 160) || undefined,
    alternates: { canonical: `/c/${c.slug}` },
    openGraph: {
      title: `${c.name} — Businesses`,
      description: c.description?.slice(0, 160) || undefined,
      url: absolute(`/c/${c.slug}`),
    },
  }
}

export default async function Page({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params
  const data = await getCategory(slug)
  if (!data) notFound()
  const c = data.category
  const jsonLd = { '@context': 'https://schema.org', '@type': 'CollectionPage', name: c.name, description: c.description ?? undefined }
  return (
    <>
      <script type="application/ld+json" id="bv-jsonld" dangerouslySetInnerHTML={{ __html: jsonLdScript(jsonLd) }} />
      <ClientPage />
    </>
  )
}
