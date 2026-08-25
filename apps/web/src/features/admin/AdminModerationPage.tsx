import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner } from '@/components/ui/Spinner'
import { toast } from '@/components/ui/Toast'
import { useDialogA11y } from '@/components/ui/Modal'

interface ReportDTO {
  id: string
  reporter_id: string
  target_type: string
  target_id: string
  reason: string
  status: string
  created_at: string
  target_snippet: string
}

/** Direct hide/restore (Batch 1: restore was API-only). */
function DirectModerationForm() {
  const qc = useQueryClient()
  const [type, setType] = useState('review')
  const [id, setId] = useState('')
  const [action, setAction] = useState<'hide' | 'restore'>('hide')

  const run = useMutation({
    mutationFn: () => api(`/admin/content/${type}/${id}/${action}`, { method: 'POST' }),
    onSuccess: () => {
      setId('')
      toast.success(action === 'hide' ? 'Content hidden' : 'Content restored')
      qc.invalidateQueries({ queryKey: ['reports'] })
    },
    onError: (e) => toast.error((e as Error).message),
  })

  return (
    <Card className="mt-2 flex flex-wrap items-end gap-2">
      <select value={type} onChange={(e) => setType(e.target.value)} className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink">
        {['review', 'comment', 'product', 'message'].map((t) => <option key={t} value={t}>{t}</option>)}
      </select>
      <input value={id} onChange={(e) => setId(e.target.value)} placeholder="Target id…" className="h-9 w-64 rounded-lg border border-border bg-surface px-3 text-sm text-ink" />
      <select value={action} onChange={(e) => setAction(e.target.value as 'hide' | 'restore')} className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink">
        <option value="hide">Hide</option>
        <option value="restore">Restore</option>
      </select>
      <Button size="sm" onClick={() => void run.mutateAsync()} disabled={!id.trim() || run.isPending}>Run</Button>
    </Card>
  )
}

/** Moderation queues (PRD §5.8.2): reports with context + actions, all audited. */
export function AdminModerationPage() {
  const qc = useQueryClient()
  const [tab, setTab] = useState<'open' | 'resolved'>('open')
  const [note, setNote] = useState('')
  const [active, setActive] = useState<ReportDTO | null>(null)
  const dialogRef = useDialogA11y(!!active, () => setActive(null))

  const { data, isLoading } = useQuery({
    queryKey: ['reports', tab],
    queryFn: () => api<{ reports: ReportDTO[] }>(`/admin/reports?status=${tab}&limit=50`),
  })

  const decide = useMutation({
    mutationFn: (action: string) =>
      api(`/admin/reports/${active!.id}/decide`, { method: 'POST', body: { action, note } }),
    onSuccess: () => {
      setActive(null)
      setNote('')
      qc.invalidateQueries({ queryKey: ['reports'] })
    },
  })

  if (isLoading) return <PageSpinner />

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Admin · Moderation</p>
          <h1 className="text-2xl font-semibold tracking-tight">Reports</h1>
          <p className="mt-1 text-sm text-ink2">Every action is written to the immutable audit trail (PRD §8.7).</p>
        </div>
        <div className="flex gap-2">
          <Button variant={tab === 'open' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('open')}>Open</Button>
          <Button variant={tab === 'resolved' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('resolved')}>Resolved</Button>
        </div>
      </div>

      <div className="space-y-2">
        {data?.reports.map((r) => (
          <Card key={r.id} hover className="cursor-pointer" onClick={() => setActive(r)}>
            <div className="flex items-center gap-3">
              <Badge tone="danger">{r.target_type}</Badge>
              <span className="font-mono text-xs text-ink3">{r.target_id.slice(0, 12)}…</span>
              <span className="ml-auto text-xs text-ink3">{new Date(r.created_at).toLocaleString()}</span>
            </div>
            <p className="mt-2 text-sm text-ink">"{r.reason}"</p>
            {r.target_snippet && <p className="mt-1 truncate text-xs text-ink3">{r.target_snippet}</p>}
          </Card>
        ))}
        {!data?.reports.length && <Card className="py-10 text-center text-sm text-ink3">All clear.</Card>}
      </div>

      <div className="mt-8">
        <p className="mono-label mb-2">Direct moderation (no report)</p>
        <p className="text-sm text-ink2">Hide or restore content by target id — used when a report is missing.</p>
        <DirectModerationForm />
      </div>

      {active && (
        <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 backdrop-blur-sm" onClick={() => setActive(null)}>
          <Card className="my-8 w-full max-w-lg">
            <div
              ref={dialogRef}
              tabIndex={-1}
              role="dialog"
              aria-modal="true"
              aria-label={`Report · ${active.target_type}`}
              onClick={(e) => e.stopPropagation()}
              className="outline-none"
            >
              <p className="mono-label mb-2">Report · {active.target_type} · {active.target_id}</p>
              <p className="text-sm text-ink2">{active.reason}</p>
              {active.target_snippet && <p className="mt-2 rounded-lg bg-surface2 p-3 text-sm text-ink2">{active.target_snippet}</p>}
              <input
                value={note}
                onChange={(e) => setNote(e.target.value)}
                placeholder="Note (shown to the user for warnings)"
                className="mt-3 h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink"
              />
              {decide.error && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{(decide.error as Error).message}</p>}
              <div className="mt-4 flex flex-wrap gap-2">
                <Button variant="secondary" size="sm" onClick={() => void decide.mutateAsync('dismiss')} disabled={decide.isPending}>Dismiss</Button>
                <Button variant="secondary" size="sm" onClick={() => void decide.mutateAsync('hide')} disabled={decide.isPending}>Hide content</Button>
                <Button variant="secondary" size="sm" onClick={() => void decide.mutateAsync('warn')} disabled={decide.isPending}>Warn author</Button>
                <Button variant="danger" size="sm" onClick={() => void decide.mutateAsync('suspend')} disabled={decide.isPending}>Suspend (7d)</Button>
              </div>
            </div>
          </Card>
        </div>
      )}
    </div>
  )
}
