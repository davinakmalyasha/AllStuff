import { useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, Loader2, Send, XCircle } from 'lucide-react'
import { api, type BusinessDTO, type VerificationDocumentDTO } from '@/lib/api'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { PageSpinner } from '@/components/ui/Spinner'
import { useAuth } from '@/stores/auth'

const STATUS_LABEL: Record<string, { label: string; tone: 'neutral' | 'attention' | 'positive' | 'danger' }> = {
  draft: { label: 'Draft', tone: 'neutral' },
  pending_review: { label: 'Pending review', tone: 'attention' },
  verified: { label: 'Verified', tone: 'positive' },
  rejected: { label: 'Rejected', tone: 'danger' },
  suspended: { label: 'Suspended', tone: 'danger' },
  paused: { label: 'Paused', tone: 'neutral' },
  closed: { label: 'Closed', tone: 'neutral' },
}

export function VerificationSettingsPage() {
  const { user } = useAuth()
  const location = useLocation()
  const justSubmitted = (location.state as { justSubmitted?: boolean } | null)?.justSubmitted
  const [flash, setFlash] = useState(justSubmitted)
  const [resubmitting, setResubmitting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (flash) setTimeout(() => setFlash(false), 4000)
  }, [flash])

  const { data, isLoading, refetch } = useQuery({
    queryKey: ['my-businesses'],
    queryFn: () => api<{ businesses: BusinessDTO[] }>('/businesses'),
    enabled: !!user,
  })

  const pick = (list: BusinessDTO[]) =>
    list.find((b) => b.status === 'pending_review' || b.status === 'rejected' || b.status === 'draft') ?? list[0]

  const business = pick(data?.businesses ?? [])
  const status = business ? STATUS_LABEL[business.status] : null

  const { data: docData } = useQuery({
    queryKey: ['docs', business?.id],
    queryFn: () => api<{ documents: VerificationDocumentDTO[] }>(`/businesses/${business!.id}/documents`),
    enabled: !!business,
  })

  const resubmit = async () => {
    if (!business) return
    setResubmitting(true)
    setError('')
    try {
      await api(`/businesses/${business.id}/resubmit`, { method: 'POST' })
      await refetch()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setResubmitting(false)
    }
  }

  if (isLoading) return <PageSpinner />
  if (!business) {
    return (
      <div className="mx-auto max-w-xl">
        <p className="mono-label mb-1">Verification</p>
        <h1 className="text-2xl font-semibold tracking-tight">No business yet</h1>
        <p className="mt-2 text-sm text-ink2">Start the registration wizard to create your storefront.</p>
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-xl">
      <p className="mono-label mb-1">Verification</p>
      <h1 className="text-2xl font-semibold tracking-tight">{business.name}</h1>

      {flash && (
        <p className="mt-4 flex items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink2">
          <CheckCircle2 className="h-4 w-4" /> Submitted for review. The admin will verify your info and documents.
        </p>
      )}
      {error && (
        <p className="mt-4 rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">{error}</p>
      )}

      <Card className="mt-4 space-y-4">
        <div className="flex items-center justify-between">
          <span className="text-sm text-ink2">Status</span>
          {status && <Badge tone={status.tone} dot>{status.label}</Badge>}
        </div>
        <div className="flex items-center justify-between">
          <span className="text-sm text-ink2">Trust level</span>
          {business.verification_level ? (
            <Badge tone={business.verification_level === 'fully_verified' ? 'attention' : 'positive'}>
              {business.verification_level === 'fully_verified' ? 'Fully Verified' : 'Verified'}
            </Badge>
          ) : (
            <span className="text-sm text-ink3">Not assigned yet</span>
          )}
        </div>
        {business.rejection_reason && (
          <div className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
            <p className="flex items-center gap-2 font-medium"><AlertTriangle className="h-4 w-4" /> Rejection reason</p>
            <p className="mt-1">{business.rejection_reason}</p>
          </div>
        )}
        <div className="flex items-center justify-between border-t border-border pt-3">
          <span className="text-sm text-ink2">Verification documents</span>
          <span className="text-sm text-ink">{docData?.documents.length ?? 0} uploaded</span>
        </div>
        {docData?.documents.map((d) => (
          <div key={d.id} className="flex items-center gap-2 text-sm">
            <span className="flex-1 text-ink">{d.kind.replace('_', ' ')}</span>
            <span className={`text-xs ${d.status === 'approved' ? 'text-ink' : 'text-ink3'}`}>{d.status}</span>
          </div>
        ))}
        {business.status === 'rejected' && (
          <Button onClick={() => void resubmit()} disabled={resubmitting} className="w-full">
            {resubmitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />} Resubmit for review
          </Button>
        )}
        {business.status === 'pending_review' && (
          <p className="flex items-center gap-2 rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink2">
            <XCircle className="h-4 w-4 text-ink3" /> In review — edits are locked until the admin decides (PRD §8.2).
          </p>
        )}
      </Card>
    </div>
  )
}
