import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, MessageSquare, Star } from 'lucide-react'
import { api } from '@/lib/api'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Confirm } from '@/components/ui/Modal'
import { ErrorNote, PageSpinner } from '@/components/ui/Spinner'
import { usePageMeta } from '@/lib/meta'
import { useAuth } from '@/stores/auth'
import { toast } from '@/components/ui/Toast'

interface PublicProfileDTO {
  id: string
  name: string
  username: string
  avatar_url: string | null
  bio: string | null
  joined_at: string
  reviews: Array<{ id: string; rating: number; text: string; created_at: string; business_name: string; business_slug: string }>
  comments: Array<{ id: string; text: string; created_at: string; business_name: string; business_slug: string }>
  collections: Array<{ id: string; name: string; slug: string; created_at: string }>
  businesses: Array<{ id: string; name: string; slug: string; logo_url: string | null; city: string; category_id: string }>
}

/** Public user profile (PRD §6.1 /u/:username). */
export function UserProfilePage() {
  const { username = '' } = useParams()
  const { user } = useAuth()

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['profile', username],
    queryFn: () => api<PublicProfileDTO>(`/u/${username}`),
  })

  usePageMeta(data ? `${data.name} (@${data.username})` : 'Profile')

  const isSelf = !!user && data && user.id === data.id

  if (isLoading) return <PageSpinner />
  if (isError) return <ErrorNote onRetry={() => void refetch()} />
  if (!data) {
    return (
      <div className="container-page flex min-h-[40vh] flex-col items-center justify-center gap-2 text-center">
        <p className="font-mono text-5xl font-semibold tracking-tight">404</p>
        <p className="text-sm text-ink2">User not found.</p>
      </div>
    )
  }

  return (
    <div className="container-page max-w-3xl py-10">
      <div className="mb-8 flex items-center gap-4">
        <div className="flex h-16 w-16 items-center justify-center rounded-full bg-accent text-2xl font-semibold text-accent-ink">
          {data.avatar_url ? <img src={data.avatar_url} alt="" className="h-16 w-16 rounded-full object-cover" /> : data.name.charAt(0).toUpperCase()}
        </div>
        <div>
          <h1 className="text-xl font-semibold tracking-tight">{data.name}</h1>
          <p className="text-sm text-ink3">@{data.username} · joined {new Date(data.joined_at).toLocaleDateString()}</p>
          {data.bio && <p className="mt-1 text-sm text-ink2">{data.bio}</p>}
        </div>
        {!isSelf && <ProfileActions userId={data.id} profileUsername={username} />}
      </div>

      {data.businesses.length > 0 && (
        <section className="mb-8">
          <h2 className="mono-label mb-3">Businesses</h2>
          <div className="grid gap-3 sm:grid-cols-2">
            {data.businesses.map((b) => (
              <Link key={b.id} to={`/b/${b.slug}`} className="card flex items-center gap-3 p-3 transition-shadow hover:shadow-cardHover">
                {b.logo_url ? <img src={b.logo_url} alt="" className="h-10 w-10 rounded-lg object-cover" /> : <span className="flex h-10 w-10 items-center justify-center rounded-lg bg-surface2 text-base font-semibold">{b.name.charAt(0)}</span>}
                <span className="min-w-0">
                  <span className="block truncate text-sm font-medium text-ink">{b.name}</span>
                  <span className="text-xs text-ink3">{b.city}</span>
                </span>
              </Link>
            ))}
          </div>
        </section>
      )}

      {data.reviews.length > 0 && (
        <section className="mb-8">
          <h2 className="mono-label mb-3">Reviews</h2>
          <div className="space-y-3">
            {data.reviews.map((r) => (
              <Card key={r.id} className="space-y-1">
                <div className="flex items-center gap-2">
                  <span className="flex items-center gap-0.5">
                    {[1, 2, 3, 4, 5].map((n) => (
                      <Star key={n} className={`h-3 w-3 ${n <= r.rating ? 'fill-current text-ink' : 'text-ink3'}`} />
                    ))}
                  </span>
                  <Link to={`/b/${r.business_slug}`} className="text-xs text-ink3 hover:text-ink">{r.business_name}</Link>
                </div>
                <p className="text-sm text-ink2">{r.text}</p>
              </Card>
            ))}
          </div>
        </section>
      )}

      {data.comments.length > 0 && (
        <section className="mb-8">
          <h2 className="mono-label mb-3">Comments</h2>
          <div className="space-y-3">
            {data.comments.map((c) => (
              <Card key={c.id}>
                <p className="text-sm text-ink2">{c.text}</p>
                <Link to={`/b/${c.business_slug}`} className="mt-1 block text-xs text-ink3 hover:text-ink">{c.business_name}</Link>
              </Card>
            ))}
          </div>
        </section>
      )}

      {data.collections.length > 0 && (
        <section>
          <h2 className="mono-label mb-3">Collections</h2>
          <div className="flex flex-wrap gap-2">
            {data.collections.map((c) => (
              <Badge key={c.id}>{c.name}</Badge>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

/** Message + block actions for other users (PRD §5.5.2). */
function ProfileActions({ userId, profileUsername }: { userId: string; profileUsername: string }) {
  const { user } = useAuth()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [messaging, setMessaging] = useState(false)
  const [confirmBlock, setConfirmBlock] = useState(false)

  const { data: blocksData } = useQuery({
    queryKey: ['blocks'],
    queryFn: () => api<{ blocked_ids: string[] }>('/blocks'),
    enabled: !!user,
  })
  const isBlocked = !!user && !!userId && (blocksData?.blocked_ids ?? []).includes(userId)

  const startChat = async () => {
    setMessaging(true)
    try {
      // POST /threads with { user_id } creates (or returns) the direct thread.
      const r = await api<{ thread: { id: string } }>('/threads', { method: 'POST', body: { user_id: userId } })
      navigate(`/me/messages/${r.thread.id}`)
    } catch {
      toast.error('Could not start conversation')
      setMessaging(false)
    }
  }

  const toggleBlock = async () => {
    if (!userId) return
    try {
      await api(`/blocks/${userId}`, { method: isBlocked ? 'DELETE' : 'POST' })
      toast.success(isBlocked ? 'User unblocked.' : 'User blocked.')
      void qc.invalidateQueries({ queryKey: ['blocks'] })
    } catch {
      toast.error('Could not update the block.')
    }
  }

  if (!user) {
    return (
      <Link to={`/login?next=${encodeURIComponent(`/u/${profileUsername}`)}`} className="ml-auto">
        <Button size="sm" variant="secondary">
          <MessageSquare className="h-4 w-4" /> Message
        </Button>
      </Link>
    )
  }

  return (
    <div className="ml-auto flex items-center gap-2">
      <Button size="sm" variant="secondary" onClick={() => void startChat()} disabled={messaging}>
        <MessageSquare className="h-4 w-4" /> Message
      </Button>
      <Button
        size="sm"
        variant="ghost"
        onClick={() => setConfirmBlock(true)}
        className="text-red-700 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-950/30"
        aria-label={isBlocked ? 'Unblock user' : 'Block user'}
      >
        <Ban className="h-4 w-4" /> {isBlocked ? 'Unblock' : 'Block'}
      </Button>
      <Confirm
        open={confirmBlock}
        onClose={() => setConfirmBlock(false)}
        onConfirm={() => void toggleBlock()}
        title={isBlocked ? 'Unblock user' : 'Block user'}
        message={
          isBlocked
            ? 'They will be able to message you again.'
            : "They won't be able to message you anymore. You can unblock later."
        }
        confirmLabel={isBlocked ? 'Unblock' : 'Block'}
        danger={!isBlocked}
      />
    </div>
  )
}
