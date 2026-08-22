import { useQuery } from '@tanstack/react-query'
import { Megaphone } from 'lucide-react'
import { api } from '@/lib/api'
import { Card } from '@/components/ui/Card'
import { PageSpinner } from '@/components/ui/Spinner'
import { usePageMeta } from '@/lib/meta'

interface FeedUpdateDTO {
  id: string
  business_id: string
  title: string
  body: string
  created_at: string
  business_name?: string
  business_slug?: string
}

/** Following feed (B6): announcements from businesses you follow. */
export function FollowingFeedPage() {
  usePageMeta('Following')
  const { data, isLoading } = useQuery({
    queryKey: ['following-feed'],
    queryFn: () => api<{ updates: FeedUpdateDTO[] }>('/me/following-feed?limit=30'),
  })

  if (isLoading) return <PageSpinner />

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-6">
        <p className="mono-label mb-1">Following</p>
        <h1 className="text-2xl font-semibold tracking-tight">Latest from businesses you follow</h1>
      </div>
      <div className="space-y-3">
        {(data?.updates ?? []).map((u) => (
          <Card key={u.id}>
            <div className="flex items-center gap-2">
              <Megaphone className="h-4 w-4 text-ink3" />
              <a href={`/b/${u.business_slug}`} className="text-xs text-ink3 hover:text-ink">{u.business_name}</a>
              <span className="ml-auto text-[10px] text-ink3">{new Date(u.created_at).toLocaleString()}</span>
            </div>
            <p className="mt-2 text-sm font-semibold text-ink">{u.title}</p>
            <p className="mt-1 text-sm text-ink2">{u.body}</p>
          </Card>
        ))}
        {(data?.updates?.length ?? 0) === 0 && (
          <Card className="py-12 text-center text-sm text-ink3">
            Follow businesses to see their announcements here.
          </Card>
        )}
      </div>
    </div>
  )
}
