import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { formatDate } from '@/lib/format'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { toast } from '@/components/ui/Toast'

interface ClaimDTO {
  id: string
  user_name: string
  user_email: string
  business_id: string | null
  name: string
  category_id: string | null
  address: string
  city: string
  country: string
  website: string
  evidence: string
  status: 'open' | 'approved' | 'rejected'
  note: string
  created_at: string
}

/** Claim queue (PRD §6.1): approve → draft business for the claimer. */
export function AdminClaimsPage() {
  const qc = useQueryClient()
  const [notes, setNotes] = useState<Record<string, string>>({})

  const { data } = useQuery({
    queryKey: ['admin-claims'],
    queryFn: () => api<{ claims: ClaimDTO[] }>('/admin/claims?limit=50'),
  })
  const claims = data?.claims ?? []

  const decide = useMutation({
    mutationFn: ({ id, decision, note }: { id: string; decision: string; note: string }) =>
      api(`/admin/claims/${id}/decide`, { method: 'POST', body: { decision, note } }),
    onSuccess: (_r, vars) => {
      qc.invalidateQueries({ queryKey: ['admin-claims'] })
      toast.success(vars.decision === 'approve' ? 'Claim approved — draft created for the claimer' : 'Claim rejected')
    },
    onError: (e) => toast.error((e as Error).message || 'Decision failed.'),
  })

  const tone = (s: string) => (s === 'approved' ? 'positive' : s === 'rejected' ? 'danger' : 'attention') as 'positive' | 'danger' | 'attention'

  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <div>
        <p className="mono-label mb-1">Ownership</p>
        <h1 className="text-2xl font-semibold tracking-tight">Business claims</h1>
      </div>

      {claims.length === 0 && <Card className="py-12 text-center text-sm text-ink3">No claims in the queue.</Card>}

      {claims.map((c) => (
        <Card key={c.id} className="space-y-2">
          <div className="flex items-center justify-between">
            <p className="text-sm font-semibold text-ink">{c.name}</p>
            <Badge tone={tone(c.status)} dot>{c.status}</Badge>
          </div>
          <p className="text-xs text-ink3">
            {c.address}, {c.city} {c.country}
            {c.website ? ` · ${c.website}` : ''}
          </p>
          <p className="text-xs text-ink2">
            Claimed by <span className="font-medium">{c.user_name}</span> ({c.user_email}) · {formatDate(c.created_at)}
          </p>
          {c.evidence && <p className="rounded-lg bg-surface2 px-3 py-2 text-sm text-ink2">“{c.evidence}”</p>}
          {c.note && <p className="text-xs text-ink3">Note: {c.note}</p>}

          {c.status === 'open' && (
            <div className="flex flex-wrap items-center gap-2 border-t border-border pt-3">
              <input
                value={notes[c.id] ?? ''}
                onChange={(e) => setNotes((n) => ({ ...n, [c.id]: e.target.value }))}
                placeholder="Note to the claimer…"
                className="h-9 min-w-40 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink"
              />
              <Button size="sm" onClick={() => void decide.mutateAsync({ id: c.id, decision: 'approve', note: notes[c.id] ?? '' })} disabled={decide.isPending}>
                Approve
              </Button>
              <Button variant="secondary" size="sm" onClick={() => void decide.mutateAsync({ id: c.id, decision: 'reject', note: notes[c.id] ?? '' })} disabled={decide.isPending}>
                Reject
              </Button>
            </div>
          )}
        </Card>
      ))}
    </div>
  )
}
