import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, type AnalyticsDTO } from '@/lib/api'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Card } from '@/components/ui/Card'
import { PageSpinner, ErrorNote } from '@/components/ui/Spinner'

const PERIODS = ['7d', '30d', 'all'] as const

export function AnalyticsPage() {
  const business = useActiveBusiness()
  const [period, setPeriod] = useState<(typeof PERIODS)[number]>('30d')

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['analytics', business?.id, period],
    queryFn: () => api<AnalyticsDTO>(`/businesses/${business!.id}/analytics?period=${period}`),
    enabled: !!business,
  })

  if (!business) {
    return <div className="mx-auto max-w-xl"><h1 className="text-2xl font-semibold tracking-tight">No business selected</h1></div>
  }
  if (isLoading) return <PageSpinner />
  if (isError)
    return (
      <div className="mx-auto max-w-4xl py-10">
        <ErrorNote message="Analytics are unavailable right now." onRetry={() => void refetch()} />
      </div>
    )

  const stats: [string, number][] = [
    ['Views', data?.views ?? 0],
    ['Likes', data?.likes ?? 0],
    ['Recommends', data?.recommends ?? 0],
    ['Comments', data?.comments ?? 0],
    ['Reviews', data?.reviews ?? 0],
    ['Saves', data?.saves ?? 0],
    ['Chat messages', data?.chat_messages ?? 0],
  ]
  const max = Math.max(1, ...(data?.views_series ?? []).map((d) => d.count))

  return (
    <div className="mx-auto max-w-4xl">
      <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="mono-label mb-1">Analytics</p>
          <h1 className="text-2xl font-semibold tracking-tight">{business.name}</h1>
        </div>
        <div className="flex rounded-lg border border-border p-0.5">
          {PERIODS.map((p) => (
            <button
              key={p}
              onClick={() => setPeriod(p)}
              className={`rounded-md px-3 py-1.5 text-xs ${period === p ? 'bg-accent text-accent-ink' : 'text-ink2'}`}
            >
              {p === 'all' ? 'All' : p}
            </button>
          ))}
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {stats.map(([label, value]) => (
          <Card key={label} className="p-4">
            <p className="mono-label">{label}</p>
            <p className="mt-1 font-mono text-2xl font-semibold tracking-tight">{value}</p>
          </Card>
        ))}
        <Card className="p-4">
          <p className="mono-label">Rating</p>
          <p className="mt-1 font-mono text-2xl font-semibold tracking-tight">
            {data?.rating_avg ? data.rating_avg.toFixed(1) : '—'}
          </p>
        </Card>
      </div>

      <Card className="mt-6 p-5">
        <p className="mono-label mb-4">Views over time</p>
        <div className="flex h-40 items-end gap-1">
          {(data?.views_series ?? []).map((d) => (
            <div key={d.day} className="group relative flex-1">
              <div
                className="w-full rounded-t bg-ink/80 transition-colors hover:bg-ink"
                style={{ height: `${Math.max(2, (d.count / max) * 100)}%` }}
                title={`${d.day}: ${d.count}`}
              />
            </div>
          ))}
        </div>
        <div className="mt-2 flex justify-between text-[10px] text-ink3">
          <span>{data?.views_series?.[0]?.day}</span>
          <span>{data?.views_series?.[data.views_series.length - 1]?.day}</span>
        </div>
      </Card>

      <Card className="mt-6 p-5">
        <p className="mono-label mb-3">Top products</p>
        {data?.top_products?.length ? (
          <div className="space-y-2">
            {data.top_products.map((p) => (
              <div key={p.product_id} className="flex items-center gap-3 text-sm">
                <span className="flex-1 truncate text-ink">{p.name}</span>
                <span className="font-mono text-xs text-ink3">{p.views} views</span>
                <span className="font-mono text-xs text-ink3">{p.likes} likes</span>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-ink3">No products yet.</p>
        )}
      </Card>

      {data?.leaderboard && (
        <p className="mt-4 text-xs text-ink3">
          Leaderboard: global #{data.leaderboard.global ?? '—'} · category #{data.leaderboard.category ?? '—'}
        </p>
      )}
    </div>
  )
}
