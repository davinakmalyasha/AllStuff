import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Megaphone } from 'lucide-react'
import { api } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { toast } from '@/components/ui/Toast'

interface UpdateDTO {
  id: string
  business_id: string
  author_id: string
  title: string
  body: string
  created_at: string
}

/** Owner announcements on the business page (B6); owners can post. */
export function UpdatesSection({ businessId, isOwner }: { businessId: string; isOwner: boolean }) {
  const qc = useQueryClient()
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')

  const { data } = useQuery({
    queryKey: ['updates', businessId],
    queryFn: () => api<{ updates: UpdateDTO[] }>(`/businesses/${businessId}/updates?limit=10`),
  })

  const post = useMutation({
    mutationFn: () => api(`/businesses/${businessId}/updates`, { method: 'POST', body: { title, body } }),
    onSuccess: () => {
      setTitle('')
      setBody('')
      qc.invalidateQueries({ queryKey: ['updates', businessId] })
      toast.success('Announcement posted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not post the announcement.'),
  })

  return (
    <section>
      <h2 className="mono-label mb-3 flex items-center gap-2"><Megaphone className="h-3.5 w-3.5" /> Announcements</h2>
      {isOwner && (
        <Card className="mb-4 space-y-2">
          <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Title (e.g. New summer menu)" className="h-9 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink" />
          <textarea rows={2} value={body} onChange={(e) => setBody(e.target.value)} placeholder="What's new? Followers will be notified…" className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink" />
          <Button size="sm" onClick={() => void post.mutateAsync()} disabled={!title.trim() || body.trim().length < 10 || post.isPending}>Post announcement</Button>
        </Card>
      )}
      <div className="space-y-3">
        {(data?.updates ?? []).map((u) => (
          <div key={u.id} className="rounded-lg border border-border p-3">
            <p className="text-sm font-semibold text-ink">{u.title}</p>
            <p className="mt-1 text-sm text-ink2">{u.body}</p>
            <p className="mt-1.5 text-[10px] text-ink3">{formatDateTime(u.created_at)}</p>
          </div>
        ))}
        {(data?.updates?.length ?? 0) === 0 && <p className="text-sm text-ink3">No announcements yet.</p>}
      </div>
    </section>
  )
}
