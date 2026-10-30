"use client"

import { useMemo, useState } from 'react'
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

/** Owner reputation cockpit: replies that are due, low ratings first, and a
 *  one-tap "ask for a review" link. The public section already supported reply
 *  edit/delete; this page exposed neither, so a typo in a public reply was
 *  permanent from the owner's side. */
export function DashboardReviewsPage() {
  const businesses = useMyBusinesses()
  const [bizId, setBizId] = useState('')
  const id = bizId || businesses[0]?.id || ''
  const active = businesses.find((b) => b.id === id)
  const qc = useQueryClient()

  const { data } = useQuery({
    queryKey: ['dashboard-reviews', id],
    queryFn: () => api<{ reviews: ReviewDTO[] }>(`/businesses/${id}/reviews?limit=100`),
    enabled: !!id,
  })
  const reviews = useMemo(() => data?.reviews ?? [], [data])

  const [replyDraft, setReplyDraft] = useState<Record<string, string>>({})
  const [editing, setEditing] = useState<string | null>(null)
  const [editDraft, setEditDraft] = useState('')
  const [filter, setFilter] = useState<'all' | 'needs' | 'low'>('all')

  const refresh = () => qc.invalidateQueries({ queryKey: ['dashboard-reviews', id] })

  const reply = useMutation({
    mutationFn: ({ reviewId, reply }: { reviewId: string; reply: string }) =>
      api(`/reviews/${reviewId}/reply`, { method: 'POST', body: { reply } }),
    onSuccess: (_r, vars) => {
      setReplyDraft((d) => ({ ...d, [vars.reviewId]: '' }))
      refresh()
      toast.success('Reply posted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not post the reply.'),
  })

  const editReply = useMutation({
    mutationFn: ({ reviewId, reply }: { reviewId: string; reply: string }) =>
      api(`/reviews/${reviewId}/reply`, { method: 'PATCH', body: { reply } }),
    onSuccess: () => {
      setEditing(null)
      refresh()
      toast.success('Reply updated')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not update the reply.'),
  })

  const deleteReply = useMutation({
    mutationFn: (reviewId: string) => api(`/reviews/${reviewId}/reply`, { method: 'DELETE' }),
    onSuccess: () => {
      refresh()
      toast.success('Reply removed')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not remove the reply.'),
  })

  const summary = useMemo(() => {
    if (reviews.length === 0) return null
    const sum = reviews.reduce((n, r) => n + r.rating, 0)
    return {
      avg: sum / reviews.length,
      count: reviews.length,
      unreplied: reviews.filter((r) => !r.reply).length,
      low: reviews.filter((r) => r.rating <= 3).length,
    }
  }, [reviews])

  // Needs a reply first, then the lowest ratings: a 1-star with no answer is
  // the review that costs bookings, and it was previously buried wherever the
  // API's default order put it.
  const visible = useMemo(() => {
    let list = reviews
    if (filter === 'needs') list = list.filter((r) => !r.reply)
    if (filter === 'low') list = list.filter((r) => r.rating <= 3)
    return [...list].sort((a, b) => {
      if (!!a.reply !== !!b.reply) return a.reply ? 1 : -1
      if (a.rating !== b.rating) return a.rating - b.rating
      return b.created_at.localeCompare(a.created_at)
    })
  }, [reviews, filter])

  const copyReviewLink = async () => {
    if (!active) return
    const url = `${window.location.origin}/b/${active.slug}?review=1#reviews`
    try {
      await navigator.clipboard.writeText(url)
      toast.success('Review link copied — send it to a happy customer')
    } catch {
      toast.error(url)
    }
  }

  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <p className="mono-label mb-1">Engagement</p>
          <h1 className="text-2xl font-semibold tracking-tight">Reviews</h1>
        </div>
        <div className="flex items-center gap-2">
          {active && (
            <Button size="sm" variant="secondary" onClick={() => void copyReviewLink()}>
              Ask for a review
            </Button>
          )}
          {businesses.length > 1 && <BusinessPicker businesses={businesses} value={id} onChange={setBizId} />}
        </div>
      </div>

      {!businesses.length && <Card className="py-12 text-center text-sm text-ink3">Register a business to see its reviews here.</Card>}

      {summary && (
        <Card className="flex flex-wrap items-center justify-between gap-3 p-4">
          <p className="text-sm text-ink">
            <span className="font-mono text-2xl font-semibold">{summary.avg.toFixed(1)}</span>
            <span className="ml-1 text-ink3">from {summary.count} review{summary.count === 1 ? '' : 's'}</span>
          </p>
          <p className="text-xs text-ink3">
            {summary.unreplied} waiting for a reply · {summary.low} at 3 stars or below
          </p>
        </Card>
      )}

      <div className="flex flex-wrap gap-1.5" role="group" aria-label="Filter reviews">
        {([['all', 'All'], ['needs', 'Needs reply'], ['low', 'Low ratings']] as const).map(([key, label]) => (
          <button
            key={key}
            onClick={() => setFilter(key)}
            aria-pressed={filter === key}
            className={`rounded-full border px-2.5 py-1 text-xs transition-colors ${
              filter === key ? 'border-ink bg-accent text-accent-ink' : 'border-border bg-surface text-ink2 hover:bg-surface2'
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      <div className="space-y-3">
        {visible.map((r) => (
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
                <div className="flex items-center justify-between gap-2">
                  <p className="text-xs font-medium text-ink">Your reply{r.reply_edited_at ? ' (edited)' : ''}</p>
                  {editing === r.id ? null : (
                    <span className="flex gap-1">
                      <button
                        className="text-xs text-ink3 underline underline-offset-2 hover:text-ink"
                        onClick={() => { setEditing(r.id); setEditDraft(r.reply ?? '') }}
                      >
                        Edit
                      </button>
                      <button
                        className="text-xs text-red-600 underline underline-offset-2 dark:text-red-400"
                        onClick={() => void deleteReply.mutateAsync(r.id)}
                        disabled={deleteReply.isPending}
                      >
                        Remove
                      </button>
                    </span>
                  )}
                </div>
                {editing === r.id ? (
                  <div className="mt-2 flex gap-2">
                    <input
                      value={editDraft}
                      onChange={(e) => setEditDraft(e.target.value)}
                      className="h-9 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink"
                      aria-label="Edit reply"
                    />
                    <Button
                      size="sm"
                      onClick={() => void editReply.mutateAsync({ reviewId: r.id, reply: editDraft })}
                      disabled={!editDraft.trim() || editReply.isPending}
                    >
                      Save
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>Cancel</Button>
                  </div>
                ) : (
                  <p className="mt-1 text-sm text-ink2">{r.reply}</p>
                )}
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
        {!!id && reviews.length > 0 && !visible.length && (
          <Card className="py-10 text-center text-sm text-ink3">Nothing matches this filter.</Card>
        )}
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
