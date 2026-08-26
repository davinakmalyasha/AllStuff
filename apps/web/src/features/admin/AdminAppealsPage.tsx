import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner } from '@/components/ui/Spinner'
import { toast } from '@/components/ui/Toast'

interface AppealDTO {
  id: string
  user_id: string
  reason: string
  status: string
  created_at: string
  name: string
  email: string
  user_status: string
}

interface AnomalyDTO {
  id: string
  user_id: string
  signal: string
  weight: number
  occurred_at: string
  business_name: string
  business_slug: string
  normal_count: number
}

/** Appeals + trending anomalies review (B3). */
export function AdminAppealsPage() {
  const qc = useQueryClient()
  const [tab, setTab] = useState<'appeals' | 'anomalies'>('appeals')

  const { data: appeals, isLoading } = useQuery({
    queryKey: ['admin-appeals'],
    queryFn: () => api<{ appeals: AppealDTO[] }>('/admin/appeals?status=open'),
  })
  const { data: anomalies } = useQuery({
    queryKey: ['admin-anomalies'],
    queryFn: () => api<{ anomalies: AnomalyDTO[] }>('/admin/anomalies'),
    enabled: tab === 'anomalies',
  })

  const decide = useMutation({
    mutationFn: ({ id, decision }: { id: string; decision: string }) =>
      api(`/admin/appeals/${id}/decide`, { method: 'POST', body: { decision } }),
    onSuccess: (_r, vars) => {
      qc.invalidateQueries({ queryKey: ['admin-appeals'] })
      toast.success(vars.decision === 'approve' ? 'Account restored' : 'Appeal rejected')
    },
    onError: (e) => toast.error((e as Error).message || 'Decision failed.'),
  })
  const resolveAnomaly = useMutation({
    mutationFn: (id: string) => api(`/admin/anomalies/${id}/resolve`, { method: 'POST' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin-anomalies'] })
      toast.success('Anomaly resolved')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not resolve the anomaly.'),
  })

  if (isLoading) return <PageSpinner />

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Admin · Reviews</p>
          <h1 className="text-2xl font-semibold tracking-tight">Appeals & anomalies</h1>
        </div>
        <div className="flex gap-2">
          <Button variant={tab === 'appeals' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('appeals')}>Appeals ({appeals?.appeals.length ?? 0})</Button>
          <Button variant={tab === 'anomalies' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('anomalies')}>Anomalies ({anomalies?.anomalies.length ?? 0})</Button>
        </div>
      </div>

      {tab === 'appeals' && (
        <div className="space-y-3">
          {appeals?.appeals.map((a) => (
            <Card key={a.id}>
              <div className="flex items-center gap-2">
                <Badge tone={a.user_status === 'suspended' ? 'attention' : 'danger'} dot>{a.user_status}</Badge>
                <span className="text-sm font-medium text-ink">{a.name}</span>
                <span className="text-xs text-ink3">{a.email}</span>
                <span className="ml-auto text-xs text-ink3">{formatDateTime(a.created_at)}</span>
              </div>
              <p className="mt-2 rounded-lg bg-surface2 p-3 text-sm text-ink2">{a.reason}</p>
              <div className="mt-3 flex gap-2">
                <Button size="sm" onClick={() => void decide.mutateAsync({ id: a.id, decision: 'approve' })} disabled={decide.isPending}>Restore account</Button>
                <Button variant="secondary" size="sm" onClick={() => void decide.mutateAsync({ id: a.id, decision: 'reject' })} disabled={decide.isPending}>Reject appeal</Button>
              </div>
            </Card>
          ))}
          {(appeals?.appeals.length ?? 0) === 0 && <Card className="py-10 text-center text-sm text-ink3">No open appeals.</Card>}
        </div>
      )}

      {tab === 'anomalies' && (
        <div className="space-y-3">
          {anomalies?.anomalies.map((x) => (
            <Card key={x.id} className="flex items-center gap-3">
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-semibold text-ink">{x.business_name} <span className="font-normal text-ink3">({x.signal} ×{x.weight})</span></p>
                <p className="text-xs text-ink3">flagged {formatDateTime(x.occurred_at)} · {x.normal_count} normal events</p>
              </div>
              <Button variant="secondary" size="sm" onClick={() => void resolveAnomaly.mutateAsync(x.id)} disabled={resolveAnomaly.isPending}>Looks fine</Button>
            </Card>
          ))}
          {(anomalies?.anomalies.length ?? 0) === 0 && <Card className="py-10 text-center text-sm text-ink3">No flagged anomalies.</Card>}
        </div>
      )}
    </div>
  )
}
