import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, Star } from 'lucide-react'
import { api, type ReviewDTO } from '@/lib/api'
import { formatDate } from '@/lib/format'
import { Card } from '@/components/ui/Card'
import { Button } from '@/components/ui/Button'
import { Confirm } from '@/components/ui/Modal'
import { toast } from '@/components/ui/Toast'

/** My reviews: every review the user wrote, with business links. */
export function MyReviewsPage() {
  const qc = useQueryClient()
  const [deleted, setDeleted] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)

  const { data } = useQuery({
    queryKey: ['me-reviews'],
    queryFn: () => api<{ reviews: ReviewDTO[] }>('/me/reviews?limit=100'),
  })
  const reviews = data?.reviews ?? []

  const remove = async (id: string) => {
    try {
      await api(`/reviews/${id}`, { method: 'DELETE' })
      setDeleted(id)
      toast.success('Review deleted')
      qc.invalidateQueries({ queryKey: ['me-reviews'] })
    } catch (e) {
      toast.error((e as Error).message || 'Could not delete the review.')
    }
  }

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Activity</p>
          <h1 className="text-2xl font-semibold tracking-tight">My reviews</h1>
        </div>
        <Link to="/me/export" className="text-sm text-ink3 hover:text-ink">Export data →</Link>
      </div>

      <div className="space-y-3">
        {reviews.length === 0 && (
          <Card className="py-12 text-center text-sm text-ink3">
            You haven't reviewed anything yet. Explore and share your experience.
          </Card>
        )}
        {reviews.map((r) => (
          <Card key={r.id} className="space-y-2">
            <div className="flex items-center justify-between">
              <div className="min-w-0">
                <Link to={`/b/${r.business_slug ?? r.business_id}`} className="text-sm font-semibold text-ink hover:underline">
                  {r.business_name || 'Business'}
                </Link>
                {r.product_id && <span className="ml-2 text-xs text-ink3">product review</span>}
              </div>
              <span className="flex items-center gap-0.5">
                {[1, 2, 3, 4, 5].map((n) => (
                  <Star key={n} className={`h-3 w-3 ${n <= r.rating ? 'fill-current text-ink' : 'text-ink3'}`} />
                ))}
              </span>
            </div>
            <p className="text-sm leading-relaxed text-ink2">{r.text}</p>
            <div className="flex items-center justify-between text-xs text-ink3">
              <span>{formatDate(r.created_at)}</span>
              {deleted !== r.id ? (
                <button onClick={() => setDeleteTarget(r.id)} className="hover:text-ink">Delete</button>
              ) : (
                <span className="italic">deleted</span>
              )}
            </div>
          </Card>
        ))}
      </div>

      <Confirm
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && void remove(deleteTarget)}
        title="Delete review"
        message="This removes your review permanently."
        confirmLabel="Delete"
        danger
      />
    </div>
  )
}

/** Data export: synchronous JSON download today. */
export function ExportPage() {
  const [busy, setBusy] = useState(false)

  const run = async () => {
    setBusy(true)
    try {
      const r = await fetch('/api/v1/me/export', { credentials: 'include' })
      if (!r.ok) throw new Error('Export failed')
      const blob = await r.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'bizverse-export.json'
      a.click()
      URL.revokeObjectURL(url)
      toast.success('Export downloaded')
    } catch (e) {
      toast.error((e as Error).message || 'Export failed. Try again later.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mx-auto max-w-2xl">
      <p className="mono-label mb-1">Privacy</p>
      <h1 className="text-2xl font-semibold tracking-tight">Export your data</h1>
      <Card className="mt-4 space-y-3">
        <p className="text-sm text-ink2">
          Download everything BizVerse stores about you: profile, reviews, comments, collections,
          messages and activity. The file is a JSON snapshot.
        </p>
        <Button onClick={() => void run()} disabled={busy}>
          <Download className="h-4 w-4" /> {busy ? 'Preparing…' : 'Download JSON'}
        </Button>
      </Card>
    </div>
  )
}
