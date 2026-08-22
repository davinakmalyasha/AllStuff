import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type NotificationDTO } from '@/lib/api'
import { NOTIF_FILTERS, NOTIF_LABELS, notifUrl } from '@/lib/notifications'
import { Card } from '@/components/ui/Card'
import { Button } from '@/components/ui/Button'

/** Full notification inbox (PRD §5.7): type filters, mark-read, deep links. */
export function NotificationsPage() {
  const qc = useQueryClient()
  const [filter, setFilter] = useState('')

  const { data } = useQuery({
    queryKey: ['notifications', filter],
    queryFn: () =>
      api<{ notifications: NotificationDTO[]; unread: number }>(
        `/notifications?limit=50&type=${encodeURIComponent(filter)}`,
      ),
  })

  const markRead = useMutation({
    mutationFn: (ids: string[]) =>
      api('/notifications/read', { method: 'POST', body: ids.length ? { ids } : { all: true } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notifications'] }),
  })

  const items = data?.notifications ?? []

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">Notifications</h1>
        {data?.unread ? (
          <Button variant="secondary" size="sm" onClick={() => void markRead.mutateAsync([])}>
            Mark all read ({data.unread})
          </Button>
        ) : null}
      </div>

      <div className="mb-4 flex flex-wrap gap-1.5">
        {NOTIF_FILTERS.map((f) => (
          <button
            key={f.key}
            onClick={() => setFilter(f.key)}
            className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
              filter === f.key
                ? 'border-accent bg-accent text-accent-ink'
                : 'border-border text-ink2 hover:bg-surface2'
            }`}
          >
            {f.label}
          </button>
        ))}
      </div>

      <Card className="divide-y divide-border">
        {items.length === 0 && <p className="px-4 py-10 text-center text-sm text-ink3">All quiet.</p>}
        {items.map((n) => {
          const url = notifUrl(n)
          const inner = (
            <>
              <div className="flex items-center gap-2">
                <p className={`font-medium text-ink ${n.is_read ? 'opacity-60' : ''}`}>
                  {NOTIF_LABELS[n.type] ?? n.type}
                </p>
                {!n.is_read && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-accent" />}
              </div>
              <p className="mt-0.5 text-xs text-ink3">
                {typeof n.payload.title === 'string' ? `${n.payload.title} · ` : ''}
                {typeof n.payload.body === 'string' ? `${n.payload.body} · ` : ''}
                {new Date(n.created_at).toLocaleString()}
              </p>
            </>
          )
          return url ? (
            <a
              key={n.id}
              href={url}
              onClick={() => {
                if (!n.is_read) void markRead.mutateAsync([n.id])
              }}
              className="block px-4 py-3 hover:bg-surface2"
            >
              {inner}
            </a>
          ) : (
            <div key={n.id} className="px-4 py-3">
              {inner}
            </div>
          )
        })}
      </Card>
    </div>
  )
}
