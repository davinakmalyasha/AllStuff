import type { Metadata } from 'next'
import { apiServer } from '@/lib/api-server'
import { absolute } from '@/lib/meta-server'
import ClientPage from './client'

export const revalidate = 120

async function getCollection(id: string) {
  try {
    return await apiServer<{ collection: { id: string; name: string; slug: string }; owner: { name: string; username: string } }>(
      `/collections/${id}`,
      { auth: false, revalidate: 120 },
    )
  } catch {
    return null
  }
}

export async function generateMetadata({ params }: { params: Promise<{ id: string }> }): Promise<Metadata> {
  const { id } = await params
  const data = await getCollection(id)
  if (!data) return { title: 'Collection' }
  const title = `${data.collection.name} · by ${data.owner.username}`
  return {
    title,
    alternates: { canonical: `/collections/${id}` },
    openGraph: { title, url: absolute(`/collections/${id}`) },
  }
}

export default async function Page() {
  return <ClientPage />
}
