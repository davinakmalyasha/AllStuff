import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { MessageSquare, Pin, Plus, Search, WifiOff } from 'lucide-react'
import { api, type ThreadListItemDTO } from '@/lib/api'
import { useDebouncedValue } from '@/lib/hooks'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner, ErrorNote } from '@/components/ui/Spinner'
import { usePageMeta } from '@/lib/meta'
import { ws } from '@/lib/ws'

export function InboxPage({ businessId }: { businessId?: string }) {
  usePageMeta('Messages')
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [connected, setConnected] = useState(true)
  const debouncedQ = useDebouncedValue(q, 250)

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['threads', businessId ?? 'all'],
    queryFn: () => api<{ threads: ThreadListItemDTO[] }>(`/threads${businessId ? `?business_id=${businessId}` : ''}`),
  })

  const { data: hits } = useQuery({
    queryKey: ['msg-search', debouncedQ],
    queryFn: () => api<{ messages: Array<{ id: number; thread_id: string; body: string | null; created_at: string }> }>(`/messages/search?q=${encodeURIComponent(debouncedQ)}`),
    enabled: debouncedQ.trim().length >= 2,
  })

  useEffect(() => {
    ws.connect()
    const off = ws.on('message.new', () => void refetch())
    const offStatus = ws.onStatusChange(setConnected)
    return () => {
      off()
      offStatus()
    }
  }, [refetch])

  if (isLoading) return <PageSpinner />
  if (isError)
    return (
      <div className="mx-auto max-w-2xl py-10">
        <ErrorNote message="Your inbox is unavailable right now." onRetry={() => void refetch()} />
      </div>
    )

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Inbox</p>
          <h1 className="text-2xl font-semibold tracking-tight">Messages</h1>
        </div>
        <button
          onClick={() => navigate('/discover')}
          className="flex items-center gap-1.5 rounded-lg border border-border px-3 py-2 text-sm text-ink2 hover:bg-surface2"
        >
          <Plus className="h-4 w-4" /> New conversation
        </button>
      </div>

      {!connected && (
        <p className="mb-3 flex items-center gap-1.5 rounded-lg border border-border bg-surface2 px-3 py-2 text-xs text-ink2">
          <WifiOff className="h-3.5 w-3.5" /> Reconnecting… live updates resume automatically.
        </p>
      )}

      <div className="relative mb-4">
        <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-ink3" />
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search all conversations…"
          className="h-10 w-full rounded-lg border border-border bg-surface pl-9 pr-3 text-sm text-ink placeholder:text-ink3 focus:border-ink"
        />
        {hits && q.trim().length >= 2 && (
          <div className="absolute z-30 mt-2 w-full overflow-hidden rounded-xl border border-border bg-surface shadow-cardHover">
            {hits.messages.length === 0 && <p className="px-4 py-3 text-sm text-ink3">No matches.</p>}
            {hits.messages.map((m) => (
              <Link key={m.id} to={`/me/messages/${m.thread_id}`} className="block px-4 py-2.5 text-sm hover:bg-surface2">
                <span className="block truncate text-ink">{m.body ?? 'media'}</span>
                <span className="text-xs text-ink3">{new Date(m.created_at).toLocaleString()}</span>
              </Link>
            ))}
          </div>
        )}
      </div>

      <div className="space-y-2">
        {(() => {
          const all = data?.threads ?? []
          const pinned = all.filter((t) => t.pinned)
          const rest = all.filter((t) => !t.pinned)
          return (
            <>
              {pinned.length > 0 && <p className="mono-label pt-1">Pinned</p>}
              {[...pinned, ...rest].map((t) => (
                <Link
                  key={t.id}
                  to={`/me/messages/${t.id}`}
                  className={`card flex items-center gap-3 p-3.5 transition-shadow hover:shadow-cardHover ${t.pinned ? 'border-ink/30' : ''}`}
                >
                  <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-surface2 text-base font-semibold text-ink2">
                    {t.type === 'business' ? (t.business_name ?? 'B').charAt(0) : (t.other_name ?? 'U').charAt(0)}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      {t.pinned && <Pin className="h-3 w-3 shrink-0 fill-current text-ink" />}
                      <p className="truncate text-sm font-semibold text-ink">
                        {t.type === 'business' ? t.business_name ?? 'Business' : t.other_name ?? 'User'}
                      </p>
                      {t.type === 'business' && <Badge>Business</Badge>}
                    </div>
                    <p className="truncate text-xs text-ink3">{t.last_body ?? 'Start the conversation'}</p>
                  </div>
                  {t.unread > 0 && (
                    <span className="flex h-5 min-w-5 items-center justify-center rounded-full bg-accent px-1.5 text-[10px] font-semibold text-accent-ink">
                      {t.unread}
                    </span>
                  )}
                  <MessageSquare className="h-4 w-4 shrink-0 text-ink3" />
                </Link>
              ))}
              {!all.length && (
                <Card className="py-12 text-center text-sm text-ink3">
                  No conversations yet. Message a business or a user to start one.
                </Card>
              )}
            </>
          )
        })()}
      </div>
    </div>
  )
}
