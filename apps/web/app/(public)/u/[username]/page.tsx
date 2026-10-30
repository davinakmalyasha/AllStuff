import type { Metadata } from 'next'
import { apiServer } from '@/lib/api-server'
import { absolute } from '@/lib/meta-server'
import ClientPage from './client'

export const revalidate = 300

interface ProfileDTO {
  name: string
  username: string
}

async function getProfile(username: string) {
  try {
    return await apiServer<ProfileDTO>(`/u/${username}`, { auth: false, revalidate: 300 })
  } catch {
    return null
  }
}

export async function generateMetadata({ params }: { params: Promise<{ username: string }> }): Promise<Metadata> {
  const { username } = await params
  const p = await getProfile(username)
  if (!p) return { title: 'Profile' }
  const title = `${p.name} (@${p.username})`
  return {
    title,
    alternates: { canonical: `/u/${p.username}` },
    openGraph: { title, url: absolute(`/u/${p.username}`) },
  }
}

export default async function Page() {
  return <ClientPage />
}
