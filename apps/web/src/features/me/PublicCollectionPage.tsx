import { useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api, type CollectionItemDTO } from '@/lib/api'
import { Card } from '@/components/ui/Card'
import { PageSpinner } from '@/components/ui/Spinner'
import { usePageMeta } from '@/lib/meta'

/** Public collection page (Batch 2) — shareable when is_public. */
export function PublicCollectionPage() {
  const { id = '' } = useParams()
  const { data, isLoading } = useQuery({
    queryKey: ['public-collection', id],
    queryFn: () => api<{ collection: { id: string; name: string; slug: string }; items: CollectionItemDTO[]; owner: { name: string; username: string } }>(`/collections/${id}`),
  })

  usePageMeta(data ? `${data.collection.name} · by ${data.owner.username}` : 'Collection')

  if (isLoading) return <PageSpinner />
  if (!data) {
    return (
      <div className="container-page flex min-h-[40vh] flex-col items-center justify-center gap-2 text-center">
        <p className="font-mono text-5xl font-semibold tracking-tight">404</p>
        <p className="text-sm text-ink2">This collection is private or doesn't exist.</p>
      </div>
    )
  }

  return (
    <div className="container-page max-w-3xl py-10">
      <h1 className="text-2xl font-semibold tracking-tight">{data.collection.name}</h1>
      <p className="mt-1 text-sm text-ink3">by <a href={`/u/${data.owner.username}`} className="hover:text-ink">{data.owner.name}</a></p>
      <div className="mt-6 space-y-2">
        {(data.items ?? []).map((item) => (
          item.target_slug ? (
            <a key={item.id} href={`/b/${item.target_slug}`} className="card flex items-center gap-3 p-3 transition-shadow hover:shadow-cardHover">
              {item.target_logo ? <img src={item.target_logo} alt="" className="h-10 w-10 rounded-lg object-cover" /> : <span className="flex h-10 w-10 items-center justify-center rounded-lg bg-surface2 text-base font-semibold">{(item.target_name ?? '?').charAt(0)}</span>}
              <span className="min-w-0">
                <span className="block truncate text-sm font-medium text-ink">{item.target_name}</span>
                {item.note && <span className="text-xs text-ink3">{item.note}</span>}
              </span>
            </a>
          ) : (
            <Card key={item.id} className="flex items-center gap-3">
              <span className="text-sm text-ink">{item.target_name ?? `${item.target_type} · ${item.target_id.slice(0, 8)}…`}</span>
            </Card>
          )
        ))}
        {(data.items?.length ?? 0) === 0 && <p className="text-sm text-ink3">This collection is empty.</p>}
      </div>
    </div>
  )
}
