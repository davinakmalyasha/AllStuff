import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ImagePlus, Pencil, Star, ThumbsDown, ThumbsUp, Trash2 } from 'lucide-react'
import { api, uploadMedia, type ReviewDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Confirm, Modal } from '@/components/ui/Modal'
import { ReportButton } from '@/components/engagement/ReportShare'
import { toast } from '@/components/ui/Toast'
import { formatDate, resetFileInput } from '@/lib/format'
import { useAuthState } from '@/stores/auth'

/** Reviews with helpful votes and owner replies (PRD Â§5.6.2). */
export function ReviewsSection({ businessId, isOwner }: { businessId: string; isOwner: boolean }) {
  const qc = useQueryClient()
  const { user } = useAuthState((s) => ({ user: s.user }))
  const [sort, setSort] = useState<'newest' | 'highest' | 'helpful'>('newest')
  const [writing, setWriting] = useState(false)
  const [rating, setRating] = useState(5)
  const [text, setText] = useState('')
  const [photoIds, setPhotoIds] = useState<string[]>([])
  const [replyingTo, setReplyingTo] = useState<string | null>(null)
  const [replyText, setReplyText] = useState('')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [deleteId, setDeleteId] = useState<string | null>(null)
  const [lightbox, setLightbox] = useState<string | null>(null)
  // Owner reply lifecycle (PRD Â§5.6.2): replies are editable/deletable.
  const [replyEdit, setReplyEdit] = useState<{ id: string; text: string } | null>(null)
  const [replyDeleteId, setReplyDeleteId] = useState<string | null>(null)

  const PAGE_SIZE = 10
  const { data } = useQuery({
    queryKey: ['reviews', businessId, sort],
    queryFn: () => api<{ reviews: ReviewDTO[]; count?: number }>(`/businesses/${businessId}/reviews?sort=${sort}&limit=${PAGE_SIZE}`),
  })
  const [page, setPage] = useState(1)
  const [extras, setExtras] = useState<ReviewDTO[]>([])
  // Sort-switch race guard: the queryFn tags each response with the sort it
  // was fetched under, so a late old-sort response can never append into the
  // new sort's list.
  const { data: more, isFetching: moreLoading } = useQuery({
    queryKey: ['reviews-more', businessId, sort, page],
    queryFn: async () => {
      const r = await api<{ reviews: ReviewDTO[] }>(`/businesses/${businessId}/reviews?sort=${sort}&limit=${PAGE_SIZE}&offset=${page * PAGE_SIZE}`)
      return { ...r, sort }
    },
    enabled: page > 1,
  })
  useEffect(() => {
    if (more?.reviews.length && more.sort === sort) setExtras((prev) => {
      const seen = new Set([...(data?.reviews ?? []).map((r) => r.id), ...prev.map((r) => r.id)])
      return [...prev, ...more.reviews.filter((r) => !seen.has(r.id))]
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [more])
  useEffect(() => {
    setPage(1)
    setExtras([])
  }, [sort])
  const reviews = [...(data?.reviews ?? []), ...extras]
  const [hasMore, setHasMore] = useState(true)
  useEffect(() => {
    setHasMore((data?.reviews?.length ?? 0) >= PAGE_SIZE)
    if ((data?.reviews?.length ?? 0) < PAGE_SIZE) setExtras([])
  }, [data])
  useEffect(() => {
    if (more) setHasMore(more.reviews.length >= PAGE_SIZE)
  }, [more])
  const avg = reviews.length ? reviews.reduce((a, r) => a + r.rating, 0) / reviews.length : null

  const refresh = () => qc.invalidateQueries({ queryKey: ['reviews', businessId] })

  const createMut = useMutation({
    mutationFn: () => api(`/businesses/${businessId}/reviews`, { method: 'POST', body: { rating, text, image_ids: photoIds } }),
    onSuccess: () => {
      setWriting(false)
      setText('')
      setPhotoIds([])
      refresh()
      toast.success('Review posted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not post the review.'),
  })

  // Helpful votes now feed back: without invalidation/optimism the count
  // froze and users clicked repeatedly believing it was broken.
  const helpfulMut = useMutation({
    mutationFn: ({ id, vote }: { id: string; vote: number }) => api(`/reviews/${id}/helpful`, { method: 'PUT', body: { vote } }),
    onMutate: ({ id, vote }) => {
      qc.setQueryData<{ reviews: ReviewDTO[] }>(['reviews', businessId, sort], (old) =>
        old ? { ...old, reviews: old.reviews.map((r) => r.id === id ? { ...r, my_vote: vote, helpful_count: Math.max(0, r.helpful_count + (vote === 0 ? -1 : r.my_vote === 0 ? 1 : 0)) } : r) } : old)
    },
    onError: () => toast.error('Could not record your vote.'),
    onSettled: () => refresh(),
  })

  const replyMut = useMutation({
    mutationFn: ({ id, reply }: { id: string; reply: string }) => api(`/reviews/${id}/reply`, { method: 'POST', body: { reply } }),
    onSuccess: () => {
      setReplyingTo(null)
      setReplyText('')
      refresh()
      toast.success('Reply posted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not post the reply.'),
  })

  // Reply edit/delete (PRD Â§5.6.2): PATCH stamps reply_edited_at.
  const replyEditMut = useMutation({
    mutationFn: ({ id, reply }: { id: string; reply: string }) => api(`/reviews/${id}/reply`, { method: 'PATCH', body: { reply } }),
    onSuccess: () => {
      setReplyEdit(null)
      refresh()
      toast.success('Reply updated')
    },
  })
  const replyDeleteMut = useMutation({
    mutationFn: (id: string) => api(`/reviews/${id}/reply`, { method: 'DELETE' }),
    onSuccess: () => {
      setReplyDeleteId(null)
      refresh()
      toast.success('Reply removed')
    },
  })

  /** Helpful toggle (PRD Â§5.6.1): clicking the active vote clears it. */
  const voteHelpful = (r: ReviewDTO, vote: number) => {
    const next = r.my_vote === vote ? 0 : vote
    void helpfulMut.mutateAsync({ id: r.id, vote: next })
  }

  const mine = reviews.find((r) => r.user_id === user?.id)

  // Rating distribution bars (Batch 2).
  const dist = [5, 4, 3, 2, 1].map((n) => ({
    star: n,
    count: reviews.filter((r) => r.rating === n).length,
  }))
  const maxDist = Math.max(1, ...dist.map((d) => d.count))

  const updateMut = useMutation({
    mutationFn: ({ id, rating, text, image_ids }: { id: string; rating: number; text: string; image_ids: string[] }) =>
      api(`/reviews/${id}`, { method: 'PATCH', body: { rating, text, image_ids } }),
    onSuccess: () => {
      setEditingId(null)
      refresh()
      toast.success('Review updated')
    },
  })

  const deleteMut = useMutation({
    mutationFn: (id: string) => api(`/reviews/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      setDeleteId(null)
      refresh()
      toast.success('Review deleted')
    },
    onError: () => toast.error('Could not delete the review.'),
  })

  return (
    <section>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="mono-label mb-1">Reviews</h2>
          {avg !== null && (
            <p className="text-sm text-ink2">
              <span className="font-mono text-lg font-semibold text-ink">{avg.toFixed(1)}</span> Â· {reviews.length} review{reviews.length === 1 ? '' : 's'}
            </p>
          )}
        </div>
        <div className="flex items-center gap-2">
          <select value={sort} onChange={(e) => setSort(e.target.value as typeof sort)} className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink">
            <option value="newest">Newest</option>
            <option value="highest">Highest</option>
            <option value="helpful">Most helpful</option>
          </select>
          {user && !mine && (
            <Button size="sm" onClick={() => setWriting(true)}>Write review</Button>
          )}
        </div>
      </div>

      {/* Rating distribution bars (Batch 2) */}
      {reviews.length > 0 && (
        <Card className="mb-4 grid grid-cols-[auto_1fr] items-center gap-x-4 gap-y-1 p-4">
          <div className="col-span-2 mb-1 flex items-baseline gap-2">
            <span className="font-mono text-3xl font-semibold">{avg?.toFixed(1)}</span>
            <span className="text-sm text-ink3">{reviews.length} reviews</span>
          </div>
          {dist.map((d) => (
            <div key={d.star} className="flex items-center gap-2">
              <span className="flex w-8 items-center gap-0.5 text-xs text-ink2">
                {d.star} <Star className="h-3 w-3 fill-current" />
              </span>
              <div className="h-2 w-40 overflow-hidden rounded-full bg-surface2">
                <div className="h-full rounded-full bg-ink/70" style={{ width: `${(d.count / maxDist) * 100}%` }} />
              </div>
              <span className="w-6 text-right text-xs text-ink3">{d.count}</span>
            </div>
          ))}
        </Card>
      )}

      {writing && (
        <Card className="mb-4 space-y-3">
          <div className="flex items-center gap-1">
            {[1, 2, 3, 4, 5].map((n) => (
              <button key={n} onClick={() => setRating(n)} aria-label={`${n} stars`} className="text-ink">
                <Star className={`h-5 w-5 ${n <= rating ? 'fill-current' : 'text-ink3'}`} />
              </button>
            ))}
          </div>
          <textarea rows={4} value={text} onChange={(e) => setText(e.target.value)} placeholder="What was it like? (10â€“2000 characters)" className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink" />
          <div className="flex flex-wrap items-center gap-2">
            {photoIds.map((id) => (
              <div key={id} className="relative">
                <img src={`/api/v1/media/${id}/file`} alt="" className="h-16 w-16 rounded-lg object-cover" />
                <button onClick={() => setPhotoIds((prev) => prev.filter((x) => x !== id))} className="absolute -right-1.5 -top-1.5 rounded-full bg-accent p-0.5 text-accent-ink" aria-label="Remove photo">âœ•</button>
              </div>
            ))}
            {photoIds.length < 6 && (
              <label className="flex h-16 w-16 cursor-pointer items-center justify-center rounded-lg border border-dashed border-border text-ink3 hover:bg-surface2" title="Add photo">
                <ImagePlus className="h-4 w-4" />
                <input type="file" accept="image/jpeg,image/png,image/webp" className="hidden" onChange={async (e) => {
                  const f = e.target.files?.[0]
                  resetFileInput(e)
                  if (!f) return
                  try {
                    const r = await uploadMedia('gallery', f)
                    setPhotoIds((prev) => [...prev, r.media.id])
                  } catch (err) {
                    toast.error((err as Error).message || 'Could not upload the photo.')
                  }
                }} />
              </label>
            )}
          </div>
          {createMut.error && <p className="text-sm text-red-600 dark:text-red-400">{(createMut.error as Error).message}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="secondary" size="sm" onClick={() => setWriting(false)}>Cancel</Button>
            <Button size="sm" onClick={() => void createMut.mutateAsync()} disabled={text.trim().length < 10 || createMut.isPending}>Post review</Button>
          </div>
        </Card>
      )}

      <div className="space-y-3">
        {reviews.length === 0 && <Card className="py-8 text-center text-sm text-ink3">No reviews yet â€” be the first.</Card>}
        {reviews.map((r) => (
          <Card key={r.id} className="space-y-2">
            <div className="flex items-center gap-2">
              <div className="flex h-8 w-8 items-center justify-center rounded-full bg-surface2 text-sm font-semibold">{r.author_name.charAt(0)}</div>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium text-ink">{r.author_name} <span className="text-xs font-normal text-ink3">@{r.author_username}</span></p>
                <div className="flex items-center gap-1">
                  {[1, 2, 3, 4, 5].map((n) => (
                    <Star key={n} className={`h-3 w-3 ${n <= r.rating ? 'fill-current text-ink' : 'text-ink3'}`} />
                  ))}
                  <span className="ml-1 text-[10px] text-ink3">{formatDate(r.created_at)}</span>
                </div>
              </div>
              <div className="flex items-center gap-1 text-xs text-ink3">
                <button
                  onClick={() => user && voteHelpful(r, 1)}
                  disabled={!user || r.user_id === user?.id}
                  className={`flex items-center gap-1 rounded px-1.5 py-1 hover:bg-surface2 disabled:opacity-50 ${r.my_vote === 1 ? 'bg-surface2 font-medium text-ink' : ''}`}
                  aria-label="Helpful"
                  aria-pressed={r.my_vote === 1}
                  title={r.my_vote === 1 ? 'Click to remove your vote' : 'Mark helpful'}
                >
                  <ThumbsUp className={`h-3 w-3 ${r.my_vote === 1 ? 'fill-current' : ''}`} /> {r.helpful_count}
                </button>
                <button
                  onClick={() => user && voteHelpful(r, -1)}
                  disabled={!user || r.user_id === user?.id}
                  className={`rounded px-1.5 py-1 hover:bg-surface2 disabled:opacity-50 ${r.my_vote === -1 ? 'bg-surface2 font-medium text-ink' : ''}`}
                  aria-label="Not helpful"
                  aria-pressed={r.my_vote === -1}
                  title={r.my_vote === -1 ? 'Click to remove your vote' : 'Mark not helpful'}
                >
                  <ThumbsDown className={`h-3 w-3 ${r.my_vote === -1 ? 'fill-current' : ''}`} />
                </button>
              </div>
            </div>
            <p className="text-sm leading-relaxed text-ink2">{r.text}</p>
            {(r.image_ids?.length ?? 0) > 0 && (
              <div className="flex flex-wrap gap-2">
                {r.image_ids!.map((id) => (
                  <img key={id} src={`/api/v1/media/${id}/file`} alt="" className="h-20 w-20 cursor-pointer rounded-lg object-cover" loading="lazy" onClick={() => setLightbox(id)} />
                ))}
              </div>
            )}
            {r.user_id === user?.id && (
              <div className="flex gap-2">
                <button onClick={() => setEditingId(r.id)} className="text-xs text-ink3 hover:text-ink"><Pencil className="inline h-3 w-3" /> edit</button>
                <button onClick={() => setDeleteId(r.id)} className="text-xs text-ink3 hover:text-ink"><Trash2 className="inline h-3 w-3" /> delete</button>
              </div>
            )}
            <ReportButton targetType="review" targetId={r.id} compact />
            {r.reply && (
              <div className="ml-6 rounded-lg border-l-2 border-ink bg-surface2 px-3 py-2">
                <div className="flex items-center justify-between">
                  <p className="text-xs font-medium text-ink">
                    Owner reply{isOwner && r.reply_edited_at ? ' (edited)' : ''}
                  </p>
                  {isOwner && (
                    <div className="flex items-center gap-1.5">
                      {!replyEdit && (
                        <>
                          <button onClick={() => setReplyEdit({ id: r.id, text: r.reply ?? '' })} className="text-xs text-ink3 hover:text-ink"><Pencil className="inline h-3 w-3" /> edit</button>
                          <button onClick={() => setReplyDeleteId(r.id)} className="text-xs text-ink3 hover:text-ink"><Trash2 className="inline h-3 w-3" /> delete</button>
                        </>
                      )}
                    </div>
                  )}
                </div>
                {replyEdit?.id === r.id ? (
                  <div className="mt-1.5 flex gap-2">
                    <input value={replyEdit.text} onChange={(e) => setReplyEdit({ id: r.id, text: e.target.value })} className="h-9 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink" />
                    <Button size="sm" onClick={() => void replyEditMut.mutateAsync({ id: r.id, reply: replyEdit.text })} disabled={!replyEdit.text.trim() || replyEditMut.isPending}>Save</Button>
                    <Button variant="secondary" size="sm" onClick={() => setReplyEdit(null)}>Cancel</Button>
                  </div>
                ) : (
                  <p className="mt-1 text-sm text-ink2">{r.reply}</p>
                )}
              </div>
            )}
            {isOwner && !r.reply && replyingTo !== r.id && (
              <Button variant="ghost" size="sm" onClick={() => setReplyingTo(r.id)}>Reply</Button>
            )}
            {replyingTo === r.id && (
              <div className="flex gap-2">
                <input value={replyText} onChange={(e) => setReplyText(e.target.value)} placeholder="Your replyâ€¦" className="h-9 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink" />
                <Button size="sm" onClick={() => void replyMut.mutateAsync({ id: r.id, reply: replyText })} disabled={!replyText.trim() || replyMut.isPending}>Send</Button>
              </div>
            )}
          </Card>
        ))}
      </div>

      {/* Edit modal â€” isolated per-review state. The old version shared the
          compose form's rating/text, so leftover drafts (or a previously
          edited review) pre-filled and could overwrite a DIFFERENT review. */}
      <EditReviewModal
        key={editingId ?? 'none'}
        review={reviews.find((x) => x.id === editingId)}
        onClose={() => setEditingId(null)}
        onSave={(payload) => updateMut.mutateAsync(payload)}
        saving={updateMut.isPending}
      />

      <Confirm
        open={!!deleteId}
        onClose={() => setDeleteId(null)}
        onConfirm={() => deleteId && void deleteMut.mutateAsync(deleteId)}
        title="Delete review"
        message="This removes your review permanently."
        confirmLabel="Delete"
        danger
      />

      <Confirm
        open={!!replyDeleteId}
        onClose={() => setReplyDeleteId(null)}
        onConfirm={() => replyDeleteId && void replyDeleteMut.mutateAsync(replyDeleteId)}
        title="Remove owner reply"
        message="Your public reply to this review will be removed."
        confirmLabel="Remove"
        danger
      />

      {/* Load more (PRD Â§5.6.2: paginated review list) */}
      {reviews.length > 0 && hasMore && (
        <div className="mt-4 text-center">
          <Button variant="secondary" size="sm" onClick={() => setPage((p) => p + 1)} disabled={moreLoading}>
            {moreLoading ? 'Loadingâ€¦' : 'Load more'}
          </Button>
        </div>
      )}

      {/* Photo lightbox */}
      <Modal open={!!lightbox} onClose={() => setLightbox(null)} title="">
        {lightbox && <img src={`/api/v1/media/${lightbox}/file`} alt="" className="w-full rounded-lg" />}
      </Modal>
    </section>
  )
}

/** Self-contained edit form: state seeds from the review on mount (keyed by
 * review id upstream) and never touches the compose form. */
function EditReviewModal({
  review,
  onClose,
  onSave,
  saving,
}: {
  review?: ReviewDTO
  onClose: () => void
  onSave: (payload: { id: string; rating: number; text: string; image_ids: string[] }) => Promise<unknown>
  saving: boolean
}) {
  const [rating, setRating] = useState(review?.rating ?? 5)
  const [text, setText] = useState(review?.text ?? '')
  return (
    <Modal open={!!review} onClose={onClose} title="Edit review">
      {review && (
        <div className="space-y-3">
          <div className="flex items-center gap-1">
            {[1, 2, 3, 4, 5].map((n) => (
              <button key={n} onClick={() => setRating(n)} aria-label={`${n} stars`} className="text-ink">
                <Star className={`h-5 w-5 ${n <= rating ? 'fill-current' : 'text-ink3'}`} />
              </button>
            ))}
          </div>
          <textarea rows={4} value={text} onChange={(e) => setText(e.target.value)} className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink" />
          <div className="flex justify-end gap-2">
            <Button variant="secondary" size="sm" onClick={onClose}>Cancel</Button>
            <Button size="sm" onClick={() => void onSave({ id: review.id, rating, text, image_ids: review.image_ids ?? [] })} disabled={saving || text.trim().length < 10}>Save</Button>
          </div>
        </div>
      )}
    </Modal>
  )
}
