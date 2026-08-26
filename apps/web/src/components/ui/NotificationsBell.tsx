import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bell } from 'lucide-react'
import { api, type NotificationDTO } from '@/lib/api'
import { NOTIF_LABELS, notifUrl } from '@/lib/notifications'
import { useAuth } from '@/stores/auth'
import { formatDateTime } from '@/lib/format'
import { ws } from '@/lib/ws'
import { useDialogA11y } from '@/components/ui/Modal'

/** Notification bell with unread badge + dropdown (PRD §5.7). Deep-links to targets. */
export function NotificationsBell() {
  const qc = useQueryClient()
  const { user } = useAuth()
  const [open, setOpen] = useState(false)
  const panelRef = useDialogA11y(open, () => setOpen(false))

  const { data } = useQuery({
    queryKey: ['notifications'],
    queryFn: () => api<{ notifications: NotificationDTO[]; unread: number }>('/notifications?limit=15'),
    enabled: !!user,
    refetchInterval: 60_000,
  })

  // Live updates: WS notification.new frames bump the badge instantly.
  useEffect(() => {
    if (!user) return
    const off = ws.on('notification.new', () => {
      void qc.invalidateQueries({ queryKey: ['notifications'] })
    })
    return () => off?.()
  }, [user, qc])

  const markRead = useMutation({
    mutationFn: (ids: string[]) => api('/notifications/read', { method: 'POST', body: ids.length ? { ids } : { all: true } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notifications'] }),
  })

  const openNotification = (n: NotificationDTO) => {
    if (!n.is_read) void markRead.mutateAsync([n.id])
    setOpen(false)
  }

  if (!user) return null
  const unread = data?.unread ?? 0

  return (
    <div className="relative">
      <button
        onClick={() => setOpen((v) => !v)}
        className="relative inline-flex h-9 w-9 items-center justify-center rounded-lg border border-border text-ink2 transition-colors hover:bg-surface2 hover:text-ink"
        aria-label={`Notifications${unread ? ` (${unread} unread)` : ''}`}
      >
        <Bell className="h-4 w-4" />
        {unread > 0 && (
          <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 text-[10px] font-semibold text-accent-ink">
            {unread > 9 ? '9+' : unread}
          </span>
        )}
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div
            ref={panelRef}
            tabIndex={-1}
            role="dialog"
            aria-modal="true"
            aria-label="Notifications"
            className="absolute right-0 top-11 z-50 w-80 overflow-hidden rounded-xl border border-border bg-surface shadow-cardHover outline-none"
          >
            <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
              <p className="mono-label">Notifications</p>
              <div className="flex items-center gap-2">
                {unread > 0 && (
                  <button onClick={() => void markRead.mutateAsync([])} className="text-xs text-ink3 hover:text-ink">
                    Mark all read
                  </button>
                )}
                <Link to="/me/notifications" onClick={() => setOpen(false)} className="text-xs text-ink3 hover:text-ink">
                  See all
                </Link>
              </div>
            </div>
            <div className="max-h-80 overflow-y-auto">
              {!data?.notifications.length && (
                <p className="px-4 py-6 text-center text-sm text-ink3">All quiet.</p>
              )}
              {data?.notifications.map((n) => {
                const url = notifUrl(n)
                const inner = (
                  <>
                    <p className="font-medium text-ink">{NOTIF_LABELS[n.type] ?? n.type}</p>
                    <p className="mt-0.5 text-xs text-ink3">
                      {typeof n.payload.title === 'string' ? `${n.payload.title} · ` : ''}
                      {typeof n.payload.body === 'string' ? `${n.payload.body} · ` : ''}
                      {formatDateTime(n.created_at)}
                    </p>
                  </>
                )
                return url ? (
                  <Link key={n.id} to={url} onClick={() => openNotification(n)} className={`block px-4 py-3 text-sm hover:bg-surface2 ${n.is_read ? 'opacity-60' : ''}`}>
                    {inner}
                  </Link>
                ) : (
                  <div key={n.id} className={`px-4 py-3 text-sm ${n.is_read ? 'opacity-60' : ''}`}>
                    {inner}
                  </div>
                )
              })}
            </div>
          </div>
        </>
      )}
    </div>
  )
}
