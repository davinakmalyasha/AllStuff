import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { api, type QuickReplyDTO } from '@/lib/api'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { toast } from '@/components/ui/Toast'
import { InboxPage } from '@/features/chat/InboxPage'

/** Business-side messaging (PRD §6.4 /dashboard/chats) with quick replies. */
export function DashboardChatsPage() {
  const business = useActiveBusiness()
  const qc = useQueryClient()
  const [text, setText] = useState('')

  const { data } = useQuery({
    queryKey: ['quick-replies', business?.id],
    queryFn: () => api<{ quick_replies: QuickReplyDTO[] }>(`/businesses/${business!.id}/quick-replies`),
    enabled: !!business,
  })

  const add = useMutation({
    mutationFn: () => api(`/businesses/${business!.id}/quick-replies`, { method: 'POST', body: { text } }),
    onSuccess: () => {
      setText('')
      qc.invalidateQueries({ queryKey: ['quick-replies', business?.id] })
      toast.success('Quick reply added')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not add the quick reply.'),
  })

  const remove = useMutation({
    mutationFn: (q: QuickReplyDTO) => api(`/businesses/${business!.id}/quick-replies/${q.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['quick-replies', business?.id] })
      toast.success('Quick reply deleted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not delete the quick reply.'),
  })

  return (
    <div className="mx-auto max-w-5xl">
      <div className="mb-6 grid gap-6 lg:grid-cols-[1fr_280px]">
        <div>
          <InboxPage businessId={business?.id} />
        </div>
        <Card className="h-fit space-y-3">
          <p className="mono-label">Quick replies</p>
          <div className="flex gap-2">
            <input
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder="e.g. Our hours are 9–5 daily…"
              className="h-9 flex-1 rounded-lg border border-border bg-surface px-2 text-sm text-ink"
            />
            <Button size="sm" onClick={() => void add.mutateAsync()} disabled={!text.trim()}>
              <Plus className="h-3.5 w-3.5" />
            </Button>
          </div>
          <div className="space-y-1.5">
            {data?.quick_replies.map((q) => (
              <div key={q.id} className="flex items-center gap-2 rounded-lg bg-surface2 px-2.5 py-2 text-sm">
                <span className="flex-1 text-ink2">{q.text}</span>
                <button onClick={() => void remove.mutateAsync(q)} className="text-ink3 hover:text-ink" aria-label="Delete quick reply">
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
            {!data?.quick_replies.length && <p className="text-xs text-ink3">Max 20 saved replies.</p>}
          </div>
        </Card>
      </div>
    </div>
  )
}
