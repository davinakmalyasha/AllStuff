import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner } from '@/components/ui/Spinner'

interface AuditActionDTO {
  id: string
  action: string
  target_type: string
  target_id: string
  reason: string
  created_at: string
  admin_name: string
}

/** Immutable audit trail (PRD §8.7) — Batch 1 gap fix. */
export function AdminAuditPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['audit'],
    queryFn: () => api<{ actions: AuditActionDTO[] }>('/admin/moderation-actions?limit=100'),
    refetchInterval: 30_000,
  })

  if (isLoading) return <PageSpinner />

  return (
    <div className="mx-auto max-w-3xl">
      <p className="mono-label mb-1">Admin · Audit</p>
      <h1 className="text-2xl font-semibold tracking-tight">Moderation trail</h1>
      <p className="mt-1 text-sm text-ink2">Append-only: every hide, warn, suspend, ban, approve, and reject (PRD §8.7).</p>

      <Card className="mt-6 divide-y divide-border p-0">
        {(data?.actions ?? []).map((a) => (
          <div key={a.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
            <Badge tone={a.action === 'hide' || a.action === 'ban' || a.action === 'suspend' ? 'danger' : a.action === 'approve' ? 'positive' : 'neutral'}>{a.action}</Badge>
            <span className="font-mono text-xs text-ink3">{a.target_type}:{a.target_id.slice(0, 10)}…</span>
            <span className="min-w-0 flex-1 truncate text-ink2">{a.reason || '—'}</span>
            <span className="text-xs text-ink3">{a.admin_name}</span>
            <span className="text-xs text-ink3">{new Date(a.created_at).toLocaleString()}</span>
          </div>
        ))}
        {(data?.actions.length ?? 0) === 0 && <p className="px-4 py-8 text-center text-sm text-ink3">No moderation actions yet.</p>}
      </Card>
    </div>
  )
}
