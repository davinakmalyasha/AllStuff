import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { BellPlus, Bookmark, MessageSquare, Star } from 'lucide-react'
import { api, type CollectionDTO, type ProductDTO, type ReviewDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { useAuthState } from '@/stores/auth'
import { toast } from '@/components/ui/Toast'
import { formatMoney, useCurrency } from '@/stores/currency'

/** Product modal (PRD §5.3.3): variants, per-product reviews, ask-about. */
export function ProductModal({
  product,
  businessId,
  isOwner,
  onClose,
}: {
  product: ProductDTO
  businessId: string
  isOwner: boolean
  onClose: () => void
}) {
  const navigate = useNavigate()
  const { user } = useAuthState((s) => ({ user: s.user }))
  const qc = useQueryClient()
  const { rates, display } = useCurrency()
  const [rating, setRating] = useState(5)
  const [text, setText] = useState('')
  const [writing, setWriting] = useState(false)

  const { data, refetch } = useQuery({
    queryKey: ['product-reviews', product.id],
    queryFn: () => api<{ reviews: ReviewDTO[] }>(`/businesses/${businessId}/reviews?product_id=${product.id}&limit=50`),
  })
  const reviews = data?.reviews ?? []
  const avg = reviews.length ? reviews.reduce((a, r) => a + r.rating, 0) / reviews.length : null

  // Variant picker: option values → matching variant (PRD §5.4.3).
  const options = product.options ?? []
  const [picks, setPicks] = useState<Record<string, string>>({})
  const matching = useMemo(
    () =>
      product.variants?.find((v) =>
        Object.entries(picks).every(([name, value]) => v.options?.[name] === value),
      ) ?? null,
    [product.variants, picks],
  )
  const shown = matching ?? product.variants?.[0] ?? null
  const price = shown?.price ?? product.base_price

  // Gallery (PRD §5.3.3): cover + additional images, click-to-swap.
  const gallery = [...new Set([product.cover_image_id, ...(product.image_ids ?? [])].filter(Boolean) as string[])]
  const [imgIdx, setImgIdx] = useState(0)

  // Rating breakdown (PRD §5.3.3).
  const dist = [5, 4, 3, 2, 1].map((n) => ({ n, count: reviews.filter((r) => r.rating === n).length }))
  const maxDist = Math.max(1, ...dist.map((d) => d.count))

  // Save-to-collection picker (PRD §5.3.3 / §6.6 collections).
  const [pickerOpen, setPickerOpen] = useState(false)
  const { data: collections } = useQuery({
    queryKey: ['my-collections'],
    queryFn: () => api<{ collections: CollectionDTO[] }>('/me/collections'),
    enabled: pickerOpen && !!user,
  })
  const savedQc = useQueryClient()
  const saveMut = useMutation({
    mutationFn: (collectionId: string) =>
      api('/me/collections/items', { method: 'POST', body: { target_type: 'product', target_id: product.id, collection_id: collectionId } }),
    onSuccess: () => {
      setPickerOpen(false)
      toast.success('Saved to collection')
      void savedQc.invalidateQueries({ queryKey: ['my-state'] })
    },
  })

  // "Ask about this" (PRD §5.3.3): open the business thread with a message
  // that already names the item so the owner has context.
  const askAbout = async () => {
    try {
      const r = await api<{ thread: { id: string } }>('/threads', { method: 'POST', body: { business_id: businessId } })
      const q = `Hi! I'd like to ask about "${product.name}"` + (price != null ? ` (${formatMoney(price, shown?.currency ?? product.currency, display, rates)})` : '')
      await api(`/threads/${r.thread.id}/messages`, { method: 'POST', body: { body: q, type: 'text', client_msg_id: crypto.randomUUID() } }).catch(() => undefined)
      navigate(`/me/messages/${r.thread.id}`)
    } catch {
      toast.error('Could not start conversation')
    }
  }

  // Save-to-collection popover dismissal (outside click + Escape).
  const pickerRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!pickerOpen) return
    const onDown = (e: MouseEvent) => {
      if (pickerRef.current && !pickerRef.current.contains(e.target as Node)) setPickerOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setPickerOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [pickerOpen])

  const createReview = useMutation({
    mutationFn: () =>
      api(`/businesses/${businessId}/reviews`, { method: 'POST', body: { rating, text, product_id: product.id } }),
    onSuccess: () => {
      setWriting(false)
      setText('')
      refetch()
      toast.success('Review posted')
    },
  })

  const helpful = useMutation({
    mutationFn: ({ id, vote }: { id: string; vote: number }) => api(`/reviews/${id}/helpful`, { method: 'PUT', body: { vote } }),
  })

  const { data: alertQuery } = useQuery({
    queryKey: ['stock-alert', product.id],
    queryFn: () => api<{ alerted: boolean }>(`/products/${product.id}/stock-alert`),
    enabled: !!user,
  })
  const toggleAlert = useMutation({
    mutationFn: (on: boolean) =>
      api(`/products/${product.id}/stock-alert`, { method: on ? 'PUT' : 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['stock-alert', product.id] }),
  })
  const alertState = alertQuery?.alerted ?? false

  const outOfStock = shown != null && 'stock_qty' in shown && !(shown as { in_stock: boolean }).in_stock

  const mine = reviews.find((r) => r.user_id === user?.id)

  return (
    <Modal open onClose={onClose} title={product.name}>
      <div className="space-y-4">
        <div className="flex gap-4">
          <div className="shrink-0">
            {gallery.length > 0 ? (
              <>
                <img src={`/api/v1/media/${gallery[Math.min(imgIdx, gallery.length - 1)]}/file`} alt={product.name} className="h-28 w-28 rounded-xl object-cover" />
                {gallery.length > 1 && (
                  <div className="mt-1.5 flex gap-1">
                    {gallery.map((id, i) => (
                      <button
                        key={id}
                        onClick={() => setImgIdx(i)}
                        aria-label={`Image ${i + 1}`}
                        className={`h-8 w-8 overflow-hidden rounded-md border ${i === imgIdx ? 'border-accent' : 'border-border opacity-70 hover:opacity-100'}`}
                      >
                        <img src={`/api/v1/media/${id}/thumb`} alt="" className="h-full w-full object-cover" />
                      </button>
                    ))}
                  </div>
                )}
              </>
            ) : (
              <div className="flex h-28 w-28 items-center justify-center rounded-xl bg-surface2 text-2xl font-semibold">{product.name.charAt(0)}</div>
            )}
          </div>
          <div className="min-w-0 flex-1">
            {product.description && <p className="text-sm text-ink2">{product.description}</p>}
            <p className="mt-2 font-mono text-sm">
              {product.call_for_price
                ? 'Call for price'
                : formatMoney(price ?? 0, shown?.currency ?? product.currency, display, rates)}
            </p>
            {shown && 'stock_qty' in shown && (shown as { stock_qty: number | null; in_stock: boolean }).stock_qty != null && (
              <p className="mt-1 text-xs text-ink3">
                {(shown as { stock_qty: number | null; in_stock: boolean }).in_stock
                  ? `${(shown as { stock_qty: number | null }).stock_qty} in stock`
                  : 'Out of stock'}
              </p>
            )}
          </div>
        </div>

        {options.length > 0 && (
          <div className="space-y-3">
            {options.map((opt) => (
              <div key={opt.id}>
                <p className="mb-1.5 text-sm font-medium text-ink">{opt.name}</p>
                <div className="flex flex-wrap gap-1.5">
                  {opt.values.map((v) => (
                    <button
                      key={v}
                      onClick={() => setPicks((p) => ({ ...p, [opt.name]: v }))}
                      className={`rounded-full border px-3 py-1 text-xs font-medium ${
                        picks[opt.name] === v ? 'border-accent bg-accent text-accent-ink' : 'border-border text-ink2 hover:bg-surface2'
                      }`}
                    >
                      {v}
                    </button>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Rating summary + breakdown bars */}
        <div className="border-t border-border pt-3">
          <div className="flex items-start justify-between gap-4">
            <div>
              {avg !== null ? (
                <>
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-lg font-semibold">{avg.toFixed(1)}</span>
                    <span className="flex items-center gap-0.5">
                      {[1, 2, 3, 4, 5].map((n) => (
                        <Star key={n} className={`h-3.5 w-3.5 ${n <= Math.round(avg) ? 'fill-current' : 'text-ink3'}`} />
                      ))}
                    </span>
                    <span className="text-xs text-ink3">{reviews.length} review{reviews.length === 1 ? '' : 's'}</span>
                  </div>
                  <div className="mt-2 space-y-0.5">
                    {dist.map((d) => (
                      <div key={d.n} className="flex items-center gap-1.5">
                        <span className="w-2 text-right font-mono text-[10px] text-ink3">{d.n}</span>
                        <div className="h-1.5 w-28 overflow-hidden rounded-full bg-surface2">
                          <div className="h-full rounded-full bg-ink/60" style={{ width: `${(d.count / maxDist) * 100}%` }} />
                        </div>
                      </div>
                    ))}
                  </div>
                </>
              ) : (
                <span className="text-xs text-ink3">No reviews yet</span>
              )}
            </div>
            <div ref={pickerRef} className="relative ml-auto flex shrink-0 flex-col items-end gap-2">
              {user && (
                <>
                  <Button variant="secondary" size="sm" onClick={() => setPickerOpen((v) => !v)}>
                    <Bookmark className={`h-3.5 w-3.5 ${pickerOpen ? 'fill-current' : ''}`} /> Save
                  </Button>
                  {pickerOpen && (
                    <div className="absolute right-0 top-9 z-40 w-56 rounded-xl border border-border bg-surface p-3 shadow-cardHover">
                      <p className="mono-label mb-2">Save to collection</p>
                      <div className="max-h-44 space-y-1 overflow-y-auto">
                        {(collections?.collections ?? []).map((c) => (
                          <button
                            key={c.id}
                            onClick={() => void saveMut.mutateAsync(c.id)}
                            className="block w-full truncate rounded-lg px-2 py-1.5 text-left text-sm text-ink hover:bg-surface2"
                          >
                            {c.name}
                          </button>
                        ))}
                        {(collections?.collections ?? []).length === 0 && (
                          <p className="px-2 py-1 text-xs text-ink3">No collections yet.</p>
                        )}
                      </div>
                    </div>
                  )}
                </>
              )}
              {outOfStock && user && (
                <Button variant="secondary" size="sm" onClick={() => void toggleAlert.mutateAsync(!alertState)} disabled={toggleAlert.isPending}>
                  <BellPlus className="h-3.5 w-3.5" /> {alertState ? 'Alert set' : 'Notify me'}
                </Button>
              )}
              {user && !mine && !isOwner && (
                <Button variant="secondary" size="sm" onClick={() => setWriting((v) => !v)}>
                  Write review
                </Button>
              )}
              {user && (
                <Button size="sm" onClick={() => void askAbout()}>
                  <MessageSquare className="h-3.5 w-3.5" /> Ask about this
                </Button>
              )}
            </div>
          </div>
        </div>

        {writing && (
          <div className="space-y-3 rounded-xl border border-border bg-surface2 p-3">
            <div className="flex items-center gap-1">
              {[1, 2, 3, 4, 5].map((n) => (
                <button key={n} onClick={() => setRating(n)} aria-label={`${n} stars`} className="text-ink">
                  <Star className={`h-5 w-5 ${n <= rating ? 'fill-current' : 'text-ink3'}`} />
                </button>
              ))}
            </div>
            <textarea rows={3} value={text} onChange={(e) => setText(e.target.value)} placeholder="What was it like? (10–2000 characters)" className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink" />
            <div className="flex justify-end gap-2">
              <Button variant="secondary" size="sm" onClick={() => setWriting(false)}>Cancel</Button>
              <Button size="sm" onClick={() => void createReview.mutateAsync()} disabled={text.trim().length < 10 || createReview.isPending}>Post review</Button>
            </div>
          </div>
        )}

        <div className="max-h-72 space-y-2 overflow-y-auto">
          {reviews.length === 0 && <p className="py-4 text-center text-sm text-ink3">No reviews for this item yet.</p>}
          {reviews.map((r) => (
            <div key={r.id} className="rounded-xl border border-border p-3">
              <div className="flex items-center gap-2">
                <p className="text-sm font-medium text-ink">{r.author_name}</p>
                <span className="flex items-center gap-0.5">
                  {[1, 2, 3, 4, 5].map((n) => (
                    <Star key={n} className={`h-3 w-3 ${n <= r.rating ? 'fill-current text-ink' : 'text-ink3'}`} />
                  ))}
                </span>
                <button
                  onClick={() => {
                    const next = r.my_vote === 1 ? 0 : 1
                    void helpful.mutateAsync({ id: r.id, vote: next }).then(() => refetch())
                  }}
                  disabled={!user || r.user_id === user?.id}
                  className={`ml-auto flex items-center gap-1 rounded px-1.5 py-1 text-xs hover:bg-surface2 disabled:opacity-50 ${r.my_vote === 1 ? 'bg-surface2 font-medium text-ink' : 'text-ink3'}`}
                  aria-label="Helpful"
                  aria-pressed={r.my_vote === 1}
                >
                  ðŸ‘ {r.helpful_count}
                </button>
              </div>
              <p className="mt-1 text-sm text-ink2">{r.text}</p>
            </div>
          ))}
        </div>
      </div>
    </Modal>
  )
}
