import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, type BusinessDTO, type ReviewDTO } from '@/lib/api'
import { formatDate } from '@/lib/format'
import { Star } from 'lucide-react'
import { Card } from '@/components/ui/Card'
import { Button } from '@/components/ui/Button'
import { toast } from '@/components/ui/Toast'
import { useAuthState } from '@/stores/auth'

function useMyBusinesses(): BusinessDTO[] {
  const { user } = useAuthState((s) => ({ user: s.user }))
  const { data } = useQuery({
    queryKey: ['my-businesses'],
    queryFn: () => api<{ businesses: BusinessDTO[] }>('/businesses'),
    enabled: !!user,
  })
  return data?.businesses ?? []
}

function BusinessPicker({ businesses, value, onChange }: { businesses: BusinessDTO[]; value: string; onChange: (id: string) => void }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)} className="h-10 rounded-lg border border-border bg-surface px-3 text-sm text-ink">
      {businesses.map((b) => (
        <option key={b.id} value={b.id}>{b.name}</option>
      ))}
    </select>
  )
}

/** Owner inbox: reviews left on your businesses (PRD §5.4.5). */
export function DashboardReviewsPage() {
  const businesses = useMyBusinesses()
  const [bizId, setBizId] = useState('')
  const id = bizId || businesses[0]?.id || ''
  const qc = useQueryClient()

  const { data } = useQuery({
    queryKey: ['dashboard-reviews', id],
    queryFn: () => api<{ reviews: ReviewDTO[] }>(`/businesses/${id}/reviews?limit=100`),
    enabled: !!id,
  })
  const reviews = data?.reviews ?? []

  const reply = useMutation({
    mutationFn: ({ reviewId, reply }: { reviewId: string; reply: string }) =>
      api(`/reviews/${reviewId}/reply`, { method: 'POST', body: { reply } }),
    onSuccess: (_r, vars) => {
      setReplyDraft((d) => ({ ...d, [vars.reviewId]: '' }))
      qc.invalidateQueries({ queryKey: ['dashboard-reviews', id] })
      toast.success('Reply posted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not post the reply.'),
  })
  const [replyDraft, setReplyDraft] = useState<Record<string, string>>({})

  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Engagement</p>
          <h1 className="text-2xl font-semibold tracking-tight">Reviews</h1>
        </div>
        {businesses.length > 1 && <BusinessPicker businesses={businesses} value={id} onChange={setBizId} />}
      </div>

      {!businesses.length && <Card className="py-12 text-center text-sm text-ink3">Register a business to see its reviews here.</Card>}

      <div className="space-y-3">
        {reviews.map((r) => (
          <Card key={r.id} className="space-y-2">
            <div className="flex items-center gap-2">
              <div className="flex h-8 w-8 items-center justify-center rounded-full bg-surface2 text-sm font-semibold">{r.author_name.charAt(0)}</div>
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-ink">{r.author_name}</p>
                <span className="flex items-center gap-0.5">
                  {[1, 2, 3, 4, 5].map((n) => (
                    <Star key={n} className={`h-3 w-3 ${n <= r.rating ? 'fill-current text-ink' : 'text-ink3'}`} />
                  ))}
                </span>
              </div>
              <span className="text-xs text-ink3">{formatDate(r.created_at)}</span>
            </div>
            <p className="text-sm leading-relaxed text-ink2">{r.text}</p>
            {r.reply ? (
              <div className="rounded-lg border-l-2 border-ink bg-surface2 px-3 py-2">
                <p className="text-xs font-medium text-ink">Your reply</p>
                <p className="mt-1 text-sm text-ink2">{r.reply}</p>
              </div>
            ) : (
              <div className="flex gap-2">
                <input
                  value={replyDraft[r.id] ?? ''}
                  onChange={(e) => setReplyDraft((d) => ({ ...d, [r.id]: e.target.value }))}
                  placeholder="Reply publicly…"
                  className="h-9 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink"
                />
                <Button size="sm" onClick={() => void reply.mutateAsync({ reviewId: r.id, reply: replyDraft[r.id] ?? '' })} disabled={!replyDraft[r.id]?.trim() || reply.isPending}>
                  Reply
                </Button>
              </div>
            )}
          </Card>
        ))}
        {!!id && !reviews.length && <Card className="py-10 text-center text-sm text-ink3">No reviews yet.</Card>}
      </div>
    </div>
  )
}

/** Owner inbox: comments on your businesses (PRD §5.4.5). */
export function DashboardCommentsPage() {
  const businesses = useMyBusinesses()
  const [bizId, setBizId] = useState('')
  const id = bizId || businesses[0]?.id || ''

  const { data } = useQuery({
    queryKey: ['dashboard-comments', id],
    queryFn: () => api<{ comments: Array<{ id: string; author_name: string; text: string; created_at: string }> }>(`/businesses/${id}/comments?limit=100`),
    enabled: !!id,
  })
  const comments = data?.comments ?? []

  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Engagement</p>
          <h1 className="text-2xl font-semibold tracking-tight">Comments</h1>
        </div>
        {businesses.length > 1 && <BusinessPicker businesses={businesses} value={id} onChange={setBizId} />}
      </div>

      {!businesses.length && <Card className="py-12 text-center text-sm text-ink3">Register a business to see its comments here.</Card>}

      <div className="space-y-3">
        {comments.map((c) => (
          <Card key={c.id} className="flex items-start gap-3">
            <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-surface2 text-sm font-semibold">{c.author_name.charAt(0)}</div>
            <div className="min-w-0 flex-1">
              <div className="flex items-center justify-between">
                <p className="text-sm font-medium text-ink">{c.author_name}</p>
                <span className="text-xs text-ink3">{formatDate(c.created_at)}</span>
              </div>
              <p className="mt-0.5 text-sm text-ink2">{c.text}</p>
            </div>
          </Card>
        ))}
        {!!id && !comments.length && <Card className="py-10 text-center text-sm text-ink3">No comments yet.</Card>}
      </div>
    </div>
  )
}
