import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { TrendingUp } from 'lucide-react'
import { api, type TrendEntryDTO } from '@/lib/api'
import { Badge } from '@/components/ui/Badge'
import { Card } from '@/components/ui/Card'
import { PageSpinner } from '@/components/ui/Spinner'
import { usePageMeta } from '@/lib/meta'

/** Leaderboards page (Batch 1): global + per-category + city. */
export function LeaderboardsPage() {
  usePageMeta('Leaderboards')
  const [window, setWindow] = useState<'24h' | '7d' | '30d'>('7d')

  const { data, isLoading } = useQuery({
    queryKey: ['leaderboards', window],
    queryFn: () => api<{ entries: TrendEntryDTO[] }>(`/leaderboards?window=${window}&scope=global&limit=50`),
  })

  const { data: cats } = useQuery({
    queryKey: ['categories'],
    queryFn: () => api<{ categories: CategoryDTO[] }>('/categories'),
  })
  const [catScope, setCatScope] = useState<string | null>(null)

  const { data: catBoard } = useQuery({
    queryKey: ['leaderboard-cat', window, catScope],
    queryFn: () => api<{ entries: TrendEntryDTO[] }>(`/leaderboards?window=${window}&scope=category:${catScope}&limit=20`),
    enabled: !!catScope,
  })

  const entries = catScope ? catBoard?.entries : data?.entries

  return (
    <div className="container-page max-w-3xl py-10">
      <div className="mb-6 flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <TrendingUp className="h-5 w-5 text-ink" />
          <h1 className="text-2xl font-semibold tracking-tight">Leaderboards</h1>
        </div>
        <div className="flex items-center gap-2">
          <select
            value={catScope ?? ''}
            onChange={(e) => setCatScope(e.target.value || null)}
            className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink"
            aria-label="Category filter"
          >
            <option value="">Global</option>
            {(cats?.categories ?? []).filter((c) => c.parent_id).map((c) => (
              <option key={c.id} value={c.id}>{c.name}</option>
            ))}
          </select>
          <div className="flex rounded-lg border border-border p-0.5">
            {(['24h', '7d', '30d'] as const).map((w) => (
              <button
                key={w}
                onClick={() => setWindow(w)}
                className={`rounded-md px-3 py-1.5 text-xs ${window === w ? 'bg-accent text-accent-ink' : 'text-ink2'}`}
              >
                {w}
              </button>
            ))}
          </div>
        </div>
      </div>

      {isLoading ? (
        <PageSpinner />
      ) : (
        <Card className="divide-y divide-border p-0">
          {(entries ?? []).map((e, i) => (
            <Link key={e.id} to={`/b/${e.slug}`} className="flex items-center gap-4 px-5 py-3.5 transition-colors hover:bg-surface2">
              <span className="w-8 font-mono text-sm text-ink3">{i + 1}</span>
              {e.logo_url ? <img src={e.logo_url} alt="" className="h-9 w-9 rounded-lg object-cover" /> : <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-surface2 text-sm font-semibold">{e.name.charAt(0)}</span>}
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium text-ink">{e.name}</span>
                <span className="text-xs text-ink3">{e.category ?? ''} · {e.city}</span>
              </span>
              {e.is_booming && <Badge tone="attention" dot>Booming</Badge>}
              {e.is_rising && <Badge tone="attention">Rising</Badge>}
              <span className="font-mono text-sm text-ink">{e.score.toFixed(1)}</span>
              {e.velocity !== 0 && <span className="w-16 text-right font-mono text-xs text-ink3">▲{e.velocity.toFixed(1)}</span>}
            </Link>
          ))}
          {(entries?.length ?? 0) === 0 && <p className="px-5 py-10 text-center text-sm text-ink3">No data yet — signals build with engagement.</p>}
        </Card>
      )}
    </div>
  )
}

interface CategoryDTO {
  id: string
  parent_id: string | null
  name: string
}
