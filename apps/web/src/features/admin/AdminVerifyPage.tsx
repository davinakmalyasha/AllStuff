import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, ExternalLink, ShieldCheck, XCircle } from 'lucide-react'
import { api, DOC_KINDS, type BusinessDTO, type VerificationDocumentDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner } from '@/components/ui/Spinner'

export function AdminVerifyPage() {
  const qc = useQueryClient()
  const [selected, setSelected] = useState<BusinessDTO | null>(null)
  const [tab, setTab] = useState<'pending' | 'rejected'>('pending')

  const { data, isLoading } = useQuery({
    queryKey: ['verify-queue', tab],
    queryFn: () => api<{ queue: BusinessDTO[] }>(`/admin/verify${tab === 'rejected' ? '?status=rejected' : ''}`),
  })

  const { data: detail } = useQuery({
    queryKey: ['verify-detail', selected?.id],
    queryFn: () =>
      api<{
        business: BusinessDTO
        documents: VerificationDocumentDTO[]
        owner: { name: string; email: string; username: string }
      }>(`/admin/verify/${selected!.id}`),
    enabled: !!selected,
  })

  const decide = useMutation({
    mutationFn: (body: { decision: 'approve' | 'reject'; level?: 'verified' | 'fully_verified'; reason?: string }) =>
      api(`/admin/verify/${selected!.id}/decide`, { method: 'POST', body }),
    onSuccess: () => {
      setSelected(null)
      qc.invalidateQueries({ queryKey: ['verify-queue'] })
    },
  })

  const suspend = useMutation({
    mutationFn: (reason: string) => api(`/admin/businesses/${selected!.id}/suspend`, { method: 'POST', body: { reason } }),
    onSuccess: () => {
      setSelected(null)
      qc.invalidateQueries({ queryKey: ['verify-queue'] })
    },
  })

  const b = detail?.business ?? selected
  const docs = detail?.documents ?? []

  return (
    <div className="mx-auto max-w-4xl">
      <div className="mb-6">
        <p className="mono-label mb-1">Admin · Verification</p>
        <h1 className="text-2xl font-semibold tracking-tight">Verification queue</h1>
        <p className="mt-1 text-sm text-ink2">Review info + documents, then approve (with trust level) or reject with a reason (PRD §5.8.1). Every decision is audited.</p>
      </div>

      <div className="mb-4 flex gap-2">
        <Button variant={tab === 'pending' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('pending')}>Pending ({data?.queue?.length ?? 0})</Button>
        <Button variant={tab === 'rejected' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('rejected')}>Rejected</Button>
      </div>

      {isLoading ? (
        <PageSpinner />
      ) : (data?.queue?.length ?? 0) > 0 ? (
        <div className="grid gap-3 lg:grid-cols-2">
          {data!.queue.map((item) => (
            <Card key={item.id} hover className="cursor-pointer" onClick={() => setSelected(item)}>
              <div className="flex items-center gap-3">
                {item.logo_url ? (
                  <img src={item.logo_url} alt="" className="h-12 w-12 rounded-lg object-cover" />
                ) : (
                  <div className="flex h-12 w-12 items-center justify-center rounded-lg bg-surface2 text-lg font-semibold">{item.name.charAt(0)}</div>
                )}
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-semibold text-ink">{item.name}</p>
                  <p className="text-xs text-ink3">{item.category_name} · {item.city}, {item.country}</p>
                </div>
                <ExternalLink className="h-4 w-4 text-ink3" />
              </div>
              <div className="mt-3 flex flex-wrap gap-1.5 text-xs">
                <Badge>{item.price_level ? "$".repeat(item.price_level) : 'no price level'}</Badge>
                <Badge>{item.contact?.phone || item.contact?.email || item.contact?.website ? 'contact ✓' : 'NO CONTACT'}</Badge>
                <Badge>{item.logo_url ? 'logo ✓' : 'NO LOGO'}</Badge>
              </div>
            </Card>
          ))}
        </div>
      ) : (
        <Card className="py-12 text-center">
          <p className="font-mono text-sm text-ink3">{tab === 'pending' ? 'Queue is clear 🎉' : 'No rejections'}</p>
        </Card>
      )}

      {selected && b && (
        <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 backdrop-blur-sm" onClick={() => setSelected(null)}>
          <Card className="my-8 w-full max-w-2xl" >
            <div onClick={(e) => e.stopPropagation()}>
              <div className="mb-4 flex items-start justify-between gap-4">
                <div>
                  <h2 className="text-lg font-semibold tracking-tight">{b.name}</h2>
                  <p className="text-sm text-ink3">by {detail?.owner.name} ({detail?.owner.email}) · /b/{b.slug}</p>
                </div>
                <button onClick={() => setSelected(null)} className="rounded-lg p-1 text-ink3 hover:bg-surface2 hover:text-ink" aria-label="Close">✕</button>
              </div>

              <dl className="grid gap-2 text-sm sm:grid-cols-2">
                <Row label="Category" value={b.category_name ?? '—'} />
                <Row label="City / Country" value={`${b.city}, ${b.country}`} />
                <Row label="Address" value={b.address} />
                <Row label="Contact" value={[b.contact?.phone, b.contact?.email].filter(Boolean).join(' · ') || '—'} />
                <Row label="Price level" value={b.price_level ? "$".repeat(b.price_level) : '—'} />
                <Row label="Coordinates" value={`${b.lat.toFixed(4)}, ${b.lng.toFixed(4)}`} />
              </dl>

              <div className="mt-4 rounded-lg bg-surface2 p-3 text-sm leading-relaxed text-ink2">{b.description}</div>

              <div className="mt-4">
                <p className="mono-label mb-2">Documents ({docs.length})</p>
                {docs.length === 0 && <p className="text-sm text-ink3">No documents uploaded — only the Verified level is possible.</p>}
                <div className="grid gap-2 sm:grid-cols-2">
                  {docs.map((d) => (
                    <div key={d.id} className="flex items-center gap-2 rounded-lg border border-border p-2.5 text-sm">
                      <span className="flex-1 text-ink">{DOC_KINDS[d.kind] ?? d.kind}</span>
                      <Badge tone={d.status === 'approved' ? 'positive' : d.status === 'rejected' ? 'danger' : 'neutral'}>{d.status}</Badge>
                      <a
                        href={`/api/v1/admin/verify/${b.id}/documents/${d.id}/file`}
                        target="_blank"
                        rel="noreferrer"
                        className="text-ink underline underline-offset-2 hover:text-ink2"
                      >
                        View
                      </a>
                    </div>
                  ))}
                </div>
              </div>

              {decide.error && (
                <p className="mt-4 rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
                  {(decide.error as Error).message}
                </p>
              )}

              <DecideForm
                pending={decide.isPending}
                hasDocs={docs.length > 0}
                onDecide={(decision, level, reason) => void decide.mutateAsync({ decision, level, reason })}
              />

              <div className="mt-4 border-t border-border pt-3">
                <p className="mono-label mb-2">Danger</p>
                <button
                  onClick={() => {
                    const reason = window.prompt('Suspension reason (shown to the owner):')
                    if (reason?.trim()) void suspend.mutateAsync(reason.trim())
                  }}
                  className="rounded-lg border border-red-300 px-3 py-2 text-xs font-medium text-red-700 hover:bg-red-50 dark:border-red-900 dark:text-red-400 dark:hover:bg-red-950/30"
                >
                  Suspend business
                </button>
              </div>
            </div>
          </Card>
        </div>
      )}
    </div>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <dt className="text-ink3">{label}</dt>
      <dd className="truncate font-medium text-ink">{value}</dd>
    </div>
  )
}

function DecideForm({
  pending,
  hasDocs,
  onDecide,
}: {
  pending: boolean
  hasDocs: boolean
  onDecide: (decision: 'approve' | 'reject', level?: 'verified' | 'fully_verified', reason?: string) => void
}) {
  const [level, setLevel] = useState<'verified' | 'fully_verified'>('verified')
  const [reason, setReason] = useState('')
  const [mode, setMode] = useState<'approve' | 'reject'>('approve')

  return (
    <div className="mt-6 border-t border-border pt-4">
      <div className="flex gap-2">
        <Button variant={mode === 'approve' ? 'primary' : 'secondary'} size="sm" onClick={() => setMode('approve')}>
          <CheckCircle2 className="h-4 w-4" /> Approve
        </Button>
        <Button variant={mode === 'reject' ? 'primary' : 'secondary'} size="sm" onClick={() => setMode('reject')}>
          <XCircle className="h-4 w-4" /> Reject
        </Button>
      </div>
      {mode === 'approve' ? (
        <>
          <label className="mt-4 block text-sm text-ink2">
            Trust level
            <select
              value={level}
              onChange={(e) => setLevel(e.target.value as 'verified' | 'fully_verified')}
              className="mt-1.5 h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink"
            >
              <option value="verified">Verified — info checked</option>
              <option value="fully_verified" disabled={!hasDocs}>Fully Verified — info + documents</option>
            </select>
          </label>
          <Button className="mt-4 w-full" disabled={pending} onClick={() => onDecide('approve', level)}>
            <ShieldCheck className="h-4 w-4" /> Approve as {level.replace('_', ' ')}
          </Button>
        </>
      ) : (
        <>
          <textarea
            rows={3}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="Rejection reason (required — shown to the owner)"
            className="mt-4 w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink"
          />
          <Button className="mt-3 w-full" variant="danger" disabled={pending || !reason.trim()} onClick={() => onDecide('reject', undefined, reason.trim())}>
            Reject with reason
          </Button>
        </>
      )}
    </div>
  )
}
