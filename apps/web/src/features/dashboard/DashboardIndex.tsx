"use client"

import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { api, type BusinessDTO } from '@/lib/api'
import { useAuthState } from '@/stores/auth'

const STATUS_LABEL: Record<string, string> = {
  draft: 'Draft',
  pending_review: 'Pending review',
  verified: 'Verified',
  rejected: 'Rejected',
  suspended: 'Suspended',
  paused: 'Paused',
  closed: 'Closed',
}
const STATUS_TONE: Record<string, 'neutral' | 'attention' | 'positive' | 'danger'> = {
  draft: 'neutral',
  pending_review: 'attention',
  verified: 'positive',
  rejected: 'danger',
  suspended: 'danger',
  paused: 'neutral',
  closed: 'neutral',
}

export function DashboardIndex() {
  const { user } = useAuthState((s) => ({ user: s.user }))
  const { data } = useQuery({
    queryKey: ['my-businesses'],
    queryFn: () => api<{ businesses: BusinessDTO[] }>('/businesses'),
    enabled: !!user,
  })
  const businesses = data?.businesses ?? []
  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Overview</p>
          <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
        </div>
        <Link to="/dashboard/register">
          <Button size="sm">
            <Plus className="h-4 w-4" /> New business
          </Button>
        </Link>
      </div>
      {businesses.length === 0 ? (
        <Card className="py-12 text-center">
          <p className="text-sm text-ink2">
            No businesses yet. Register your first storefront — it takes about 10 minutes and it&apos;s free forever.
          </p>
          <Link to="/dashboard/register" className="mt-4 inline-block">
            <Button>Start registration</Button>
          </Link>
        </Card>
      ) : (
        <div className="grid gap-3">
          {businesses.map((b) => (
            <Link key={b.id} to="/dashboard/settings/verification" className="card flex items-center gap-3 p-4 transition-shadow hover:shadow-cardHover">
              {b.logo_url ? (
                <img src={b.logo_url} alt="" className="h-11 w-11 rounded-lg object-cover" />
              ) : (
                <div className="flex h-11 w-11 items-center justify-center rounded-lg bg-surface2 text-base font-semibold">
                  {b.name.charAt(0)}
                </div>
              )}
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-semibold text-ink">{b.name}</p>
                <p className="text-xs text-ink3">/{b.slug}</p>
              </div>
              <Badge tone={STATUS_TONE[b.status] ?? 'neutral'} dot>
                {STATUS_LABEL[b.status] ?? b.status}
              </Badge>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
