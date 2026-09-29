import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Camera, Link2 } from 'lucide-react'
import { api, uploadMedia } from '@/lib/api'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { useAuthState } from '@/stores/auth'
import { ProfileEditor } from '@/features/me/ProfileEditor'
import { Button } from '@/components/ui/Button'
import { toast } from '@/components/ui/Toast'
import { formatDate, resetFileInput } from '@/lib/format'

interface SavedSearchDTO {
  id: string
  name: string
  query: Record<string, unknown>
  notify_daily: boolean
  created_at: string
}

/** Rebuild the FULL /discover query string a saved search stores — mirrors
 *  what DiscoverPage reads back from the URL on load. */
function savedSearchHref(query: Record<string, unknown>): string {
  const p = new URLSearchParams()
  if (query.q != null && String(query.q) !== '') p.set('q', String(query.q))
  if (typeof query.city === 'string' && query.city) p.set('city', query.city)
  for (const c of Array.isArray(query.category) ? query.category : []) p.append('category', String(c))
  for (const v of Array.isArray(query.price_level) ? query.price_level : []) p.append('price_level', String(v))
  if (typeof query.min_rating === 'number' && query.min_rating > 0) p.set('min_rating', String(query.min_rating))
  if (query.open_now === true) p.set('open_now', 'true')
  if (query.verified_only === true) p.set('verified_only', 'true')
  if (query.fully_verified === true) p.set('fully_verified', 'true')
  if (query.has_chat === true) p.set('has_chat', 'true')
  if (typeof query.sort === 'string' && query.sort && query.sort !== 'trending') p.set('sort', query.sort)
  const qs = p.toString()
  return qs ? `/discover?${qs}` : '/discover'
}

export function MePage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { user, fetchMe, logout } = useAuthState((s) => ({ user: s.user, fetchMe: s.fetchMe, logout: s.logout }))
  const qc = useQueryClient()

  const { data: savedSearches } = useQuery({
    queryKey: ['saved-searches'],
    queryFn: () => api<{ searches: SavedSearchDTO[] }>('/me/saved-searches'),
    enabled: !!user,
  })

  const removeSearch = useMutation({
    mutationFn: (id: string) => api(`/me/saved-searches/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['saved-searches'] })
      toast.success('Saved search deleted')
    },
    onError: (e) => toast.error((e as Error).message),
  })

  const toggleAlert = useMutation({
    mutationFn: ({ id, on }: { id: string; on: boolean }) =>
      api(`/me/saved-searches/${id}`, { method: 'PATCH', body: { notify_daily: on } }),
    onSuccess: (_r, vars) => {
      qc.invalidateQueries({ queryKey: ['saved-searches'] })
      toast.success(vars.on ? 'Daily alerts enabled' : 'Daily alerts disabled')
    },
    onError: (e) => toast.error((e as Error).message),
  })

  const uploadAvatar = async (file: File) => {
    try {
      const r = await uploadMedia('avatar', file)
      await api('/me', { method: 'PATCH', body: { avatar_url: r.media.url } })
      await fetchMe()
      toast.success('Avatar updated')
    } catch (e) {
      toast.error((e as Error).message || 'Could not update avatar.')
    }
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
            <input type="file" accept="image/jpeg,image/png,image/webp" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; resetFileInput(e); if (f) void uploadAvatar(f) }} />
          </label>
        </div>
        <div className="min-w-0">
          <h1 className="text-xl font-semibold tracking-tight">{user.name}</h1>
          <Link to={`/u/${user.username}`} className="text-sm text-ink3 hover:text-ink">@{user.username} <Link2 className="ml-0.5 inline h-3 w-3" /></Link>
        </div>
        <div className="ml-auto flex items-center gap-2">
          {user.email_verified ? (
            <Badge tone="positive">Verified</Badge>
          ) : (
            <Badge tone="attention" dot>Verify email</Badge>
          )}
        </div>
      </div>

      <ProfileEditor user={user} />

      <Card className="mb-4">
        <dl className="grid gap-4 sm:grid-cols-2">
          <div>
            <dt className="mono-label">{t('auth.email')}</dt>
            <dd className="mt-1 text-sm text-ink">{user.email}</dd>
          </div>
          <div>
            <dt className="mono-label">Member since</dt>
            <dd className="mt-1 text-sm text-ink">
              {formatDate(user.created_at)}
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
            <Link to={savedSearchHref(s.query)} className="text-xs text-ink3 hover:text-ink">Open</Link>
            <button onClick={() => void removeSearch.mutateAsync(s.id)} className="text-xs text-ink3 hover:text-ink" aria-label="Delete search">âœ•</button>
          </div>
        ))}
        {(savedSearches?.searches?.length ?? 0) === 0 && <p className="text-sm text-ink3">Save a search from the Discover page to find it here.</p>}
      </Card>

      <div className="flex items-center justify-between">
        <div className="flex gap-2">
          <Link to="/me/collections"><Button variant="secondary" size="sm">Collections</Button></Link>
          <Link to="/me/notifications"><Button variant="secondary" size="sm">Notifications</Button></Link>
          <Link to="/me/reviews"><Button variant="secondary" size="sm">Reviews</Button></Link>
          <Link to="/me/security"><Button variant="secondary" size="sm">Security</Button></Link>
          <Link to="/me/messages"><Button variant="secondary" size="sm">Messages</Button></Link>
        </div>
        {/* Full client-side logout: clears the query cache, closes the WS and
            resets per-account stores, then routes away (SPA reload avoided). */}
        <Button
          variant="secondary"
          onClick={() =>
            void logout().then(() => navigate('/login', { replace: true }))
          }
        >
          {t('nav.logout')}
        </Button>
      </div>
    </div>
  )
}
