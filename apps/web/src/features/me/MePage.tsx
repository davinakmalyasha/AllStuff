import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Camera, Link2 } from 'lucide-react'
import { api, uploadMedia } from '@/lib/api'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { useAuth } from '@/stores/auth'
import { Button } from '@/components/ui/Button'
import { toast } from '@/components/ui/Toast'

interface SavedSearchDTO {
  id: string
  name: string
  query: Record<string, unknown>
  notify_daily: boolean
  created_at: string
}

export function MePage() {
  const { t } = useTranslation()
  const { user, fetchMe, logout } = useAuth()
  const qc = useQueryClient()

  const { data: savedSearches } = useQuery({
    queryKey: ['saved-searches'],
    queryFn: () => api<{ searches: SavedSearchDTO[] }>('/me/saved-searches'),
    enabled: !!user,
  })

  const removeSearch = useMutation({
    mutationFn: (id: string) => api(`/me/saved-searches/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['saved-searches'] }),
  })

  const toggleAlert = useMutation({
    mutationFn: ({ id, on }: { id: string; on: boolean }) =>
      api(`/me/saved-searches/${id}`, { method: 'PATCH', body: { notify_daily: on } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['saved-searches'] }),
  })

  const uploadAvatar = async (file: File) => {
    const r = await uploadMedia('avatar', file)
    await api('/me', { method: 'PATCH', body: { avatar_url: r.media.url } })
    await fetchMe()
    toast.success('Avatar updated')
  }

  if (!user) return null

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-8 flex items-center gap-4">
        <div className="group relative">
          <div className="flex h-14 w-14 items-center justify-center overflow-hidden rounded-full bg-accent text-lg font-semibold text-accent-ink">
            {user.avatar_url ? <img src={user.avatar_url} alt="" className="h-full w-full object-cover" /> : user.name.charAt(0).toUpperCase()}
          </div>
          <label className="absolute -bottom-1 -right-1 flex h-6 w-6 cursor-pointer items-center justify-center rounded-full border border-border bg-surface text-ink3 shadow-card hover:text-ink" title="Change avatar">
            <Camera className="h-3 w-3" />
            <input type="file" accept="image/jpeg,image/png,image/webp" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) void uploadAvatar(f) }} />
          </label>
        </div>
        <div className="min-w-0">
          <h1 className="text-xl font-semibold tracking-tight">{user.name}</h1>
          <a href={`/u/${user.username}`} className="text-sm text-ink3 hover:text-ink">@{user.username} <Link2 className="ml-0.5 inline h-3 w-3" /></a>
        </div>
        <div className="ml-auto flex items-center gap-2">
          {user.email_verified ? (
            <Badge tone="positive">Verified</Badge>
          ) : (
            <Badge tone="attention" dot>Verify email</Badge>
          )}
        </div>
      </div>

      <Card className="mb-4">
        <dl className="grid gap-4 sm:grid-cols-2">
          <div>
            <dt className="mono-label">{t('auth.email')}</dt>
            <dd className="mt-1 text-sm text-ink">{user.email}</dd>
          </div>
          <div>
            <dt className="mono-label">{t('me.timezone')}</dt>
            <dd className="mt-1 text-sm text-ink">{user.timezone}</dd>
          </div>
          <div>
            <dt className="mono-label">{t('me.bio')}</dt>
            <dd className="mt-1 text-sm text-ink2">{user.bio ?? '—'}</dd>
          </div>
          <div>
            <dt className="mono-label">Member since</dt>
            <dd className="mt-1 text-sm text-ink">
              {new Date(user.created_at).toLocaleDateString()}
            </dd>
          </div>
        </dl>
      </Card>

      {/* Saved searches */}
      <Card className="mb-4 space-y-2">
        <p className="mono-label">Saved searches</p>
        {(savedSearches?.searches ?? []).map((s) => (
          <div key={s.id} className="flex items-center gap-2 text-sm">
            <span className="flex-1 truncate font-medium text-ink">{s.name}</span>
            <label className="flex items-center gap-1.5 text-xs text-ink3" title="Email me daily when new businesses match">
              <input
                type="checkbox"
                checked={s.notify_daily}
                onChange={(e) => void toggleAlert.mutateAsync({ id: s.id, on: e.target.checked })}
                className="h-3.5 w-3.5 accent-black dark:accent-white"
              />
              Daily alert
            </label>
            <a href={`/discover?q=${encodeURIComponent(String(s.query?.q ?? ''))}`} className="text-xs text-ink3 hover:text-ink">Open</a>
            <button onClick={() => void removeSearch.mutateAsync(s.id)} className="text-xs text-ink3 hover:text-ink" aria-label="Delete search">✕</button>
          </div>
        ))}
        {(savedSearches?.searches?.length ?? 0) === 0 && <p className="text-sm text-ink3">Save a search from the Discover page to find it here.</p>}
      </Card>

      <div className="flex items-center justify-between">
        <div className="flex gap-2">
          <a href="/me/collections"><Button variant="secondary" size="sm">Collections</Button></a>
          <a href="/me/notifications"><Button variant="secondary" size="sm">Notifications</Button></a>
          <a href="/me/reviews"><Button variant="secondary" size="sm">Reviews</Button></a>
          <a href="/me/security"><Button variant="secondary" size="sm">Security</Button></a>
          <a href="/me/messages"><Button variant="secondary" size="sm">Messages</Button></a>
        </div>
        <Button variant="secondary" onClick={() => void logout()}>
          {t('nav.logout')}
        </Button>
      </div>
    </div>
  )
}
