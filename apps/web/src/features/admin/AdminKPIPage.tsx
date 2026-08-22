import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { Card } from '@/components/ui/Card'
import { PageSpinner } from '@/components/ui/Spinner'
import { usePageMeta } from '@/lib/meta'

interface KPIDTO {
  users: number
  businesses: number
  verified_businesses: number
  pending_reviews: number
  reviews: number
  comments: number
  messages: number
  open_reports: number
  registrations_14d: { day: string; count: number }[]
}

/** Admin KPI dashboard (PRD §5.8.6). */
export function AdminKPIPage() {
  usePageMeta('Admin · Analytics')
  const { data, isLoading } = useQuery({
    queryKey: ['admin-kpis'],
    queryFn: () => api<KPIDTO>(`/admin/kpis`),
  })

  if (isLoading) return <PageSpinner />
  const d = data as KPIDTO

  const stats: [string, number][] = [
    ['Users', d.users],
    ['Businesses', d.businesses],
    ['Verified', d.verified_businesses],
    ['Pending review', d.pending_reviews],
    ['Reviews', d.reviews],
    ['Comments', d.comments],
    ['Messages', d.messages],
    ['Open reports', d.open_reports],
  ]
  const max = Math.max(1, ...(d.registrations_14d ?? []).map((r) => r.count))
  const verifiedRate = d.businesses ? Math.round((d.verified_businesses / d.businesses) * 100) : 0

  return (
    <div className="mx-auto max-w-4xl">
      <p className="mono-label mb-1">Admin · Overview</p>
      <h1 className="text-2xl font-semibold tracking-tight">Platform analytics</h1>
      <p className="mt-1 text-sm text-ink2">Verification funnel: {d.pending_reviews} waiting · {verifiedRate}% of businesses verified.</p>

      <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
        {stats.map(([label, value]) => (
          <Card key={label} className="p-4">
            <p className="mono-label">{label}</p>
            <p className="mt-1 font-mono text-2xl font-semibold tracking-tight">{value}</p>
          </Card>
        ))}
      </div>

      <Card className="mt-6 p-5">
        <p className="mono-label mb-4">Registrations · last 14 days</p>
        <div className="flex h-36 items-end gap-1">
          {(d.registrations_14d ?? []).map((r) => (
            <div key={r.day} className="flex-1 rounded-t bg-ink/80" style={{ height: `${Math.max(3, (r.count / max) * 100)}%` }} title={`${r.day}: ${r.count}`} />
          ))}
        </div>
        {(d.registrations_14d?.length ?? 0) === 0 && <p className="text-sm text-ink3">No registrations in this window.</p>}
      </Card>
    </div>
  )
}
