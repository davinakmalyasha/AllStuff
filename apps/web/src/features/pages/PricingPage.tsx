import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { api, type PlanDTO } from '@/lib/api'
import { useAuth } from '@/stores/auth'
import { Card } from '@/components/ui/Card'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner, ErrorNote } from '@/components/ui/Spinner'
import { toast } from '@/components/ui/Toast'

/**
 * Public pricing page.
 *
 * Checkout needs a business, so this page never starts a Stripe session
 * directly: an unauthenticated visitor is sent to register, and an owner
 * without a business is sent to the wizard. Only the owner's own billing
 * surface (BillingPage) knows which business is being paid for.
 */
export function PricingPage() {
  const navigate = useNavigate()
  const user = useAuth((s) => s.user)

  const plans = useQuery({
    queryKey: ['plans'],
    queryFn: () => api<{ plans: PlanDTO[]; enabled: boolean }>('/plans'),
    staleTime: 5 * 60 * 1000,
  })

  const businesses = useQuery({
    queryKey: ['my-businesses'],
    queryFn: () => api<{ businesses: { id: string; name: string }[] }>('/businesses'),
    enabled: !!user,
  })

  const begin = useMutation({
    mutationFn: (planId: string) => {
      const b = businesses.data?.businesses?.[0]
      if (!b) throw new Error('Create a business before upgrading.')
      return api<{ url: string }>(`/businesses/${b.id}/billing/checkout`, {
        method: 'POST',
        body: { plan_id: planId },
      })
    },
    onSuccess: (res) => {
      if (res.url) window.location.href = res.url
    },
    onError: (e: Error) => toast.error(e.message),
  })

  if (plans.isLoading) return <PageSpinner label="Loading plans…" />
  if (plans.isError) {
    return (
      <div className="mx-auto max-w-4xl py-10">
        <ErrorNote message="Plans are unavailable right now." onRetry={() => void plans.refetch()} />
      </div>
    )
  }

  const list = plans.data?.plans ?? []
  const enabled = plans.data?.enabled ?? false

  function choose(plan: PlanDTO) {
    if (!user) {
      navigate('/register?next=/pricing')
      return
    }
    if (businesses.data?.businesses?.length) {
      begin.mutate(plan.id)
      return
    }
    navigate('/dashboard/new')
  }

  return (
    <div className="mx-auto max-w-5xl px-4 py-12">
      <div className="mb-10 text-center">
        <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">Pricing</h1>
        <p className="mx-auto mt-3 max-w-2xl text-ink2">
          Every business gets a free storefront. Upgrade when you want to be found first and understand your
          traffic.
        </p>
      </div>

      {!enabled && (
        <div
          role="status"
          className="mb-6 rounded-lg border border-border bg-surface2 px-4 py-3 text-center text-sm text-ink2"
        >
          Payments are not configured on this deployment, so upgrades are unavailable. Everything below still
          describes what each plan includes.
        </div>
      )}

      <div className="grid grid-cols-1 gap-5 md:grid-cols-3">
        {list.map((plan) => (
          <PlanCard
            key={plan.id}
            plan={plan}
            featured={plan.id === 'growth'}
            disabled={!enabled || !plan.purchasable || begin.isPending}
            pending={begin.isPending}
            onChoose={() => choose(plan)}
          />
        ))}
      </div>

      <p className="mt-8 text-center text-xs text-ink2">
        Prices exclude applicable taxes. Cancel any time from your billing settings.
      </p>
    </div>
  )
}

function PlanCard({
  plan,
  featured,
  disabled,
  pending,
  onChoose,
}: {
  plan: PlanDTO
  featured: boolean
  disabled: boolean
  pending: boolean
  onChoose: () => void
}) {
  const isFree = plan.price_cents === 0
  return (
    <Card className={`flex flex-col ${featured ? 'border-ink shadow-cardHover' : ''}`}>
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-lg font-semibold">{plan.name}</h2>
        {featured && <Badge>Popular</Badge>}
      </div>
      <p className="mt-3 text-3xl font-semibold tracking-tight">
        {isFree ? 'Free' : (plan.price_cents / 100).toFixed(0)}
        {!isFree && <span className="ml-1 text-sm font-normal text-ink2">{plan.currency} / month</span>}
      </p>
      <p className="mt-2 min-h-[2.5rem] text-sm text-ink2">{plan.description}</p>

      <ul className="mt-4 flex-1 space-y-1.5 text-sm">
        {describe(plan).map((line) => (
          <li key={line} className="flex items-start gap-2">
            <span aria-hidden className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-ink" />
            <span>{line}</span>
          </li>
        ))}
      </ul>

      <Button
        className="mt-5"
        fullWidth
        variant={featured ? 'primary' : 'secondary'}
        disabled={isFree || disabled}
        onClick={onChoose}
      >
        {isFree ? 'Current default' : !plan.purchasable ? 'Not available yet' : pending ? 'Starting…' : 'Choose ' + plan.name}
      </Button>
    </Card>
  )
}

/**
 * Renders the entitlement list in product language. The raw strings
 * (`product_limit:5`) are a storage format, not something to show a customer.
 */
function describe(plan: PlanDTO): string[] {
  const limits: string[] = []
  const flags: string[] = []

  for (const raw of plan.entitlements) {
    const [name, value] = raw.split(':')
    switch (name) {
      case 'product_limit':
        limits.push(`Up to ${value} published products`)
        break
      case 'gallery_limit':
        limits.push(`Up to ${value} gallery photos`)
        break
      case 'team_seats':
        limits.push(value === '1' ? 'Single owner seat' : `Up to ${value} team seats`)
        break
      case 'analytics':
        flags.push('Owner analytics')
        break
      case 'analytics_advanced':
        flags.push('Advanced analytics and funnels')
        break
      case 'featured_placement':
        flags.push('Featured placement')
        break
      case 'verified_badge':
        flags.push('Priority verification review')
        break
      case 'api_access':
        flags.push('Public read API keys')
        break
      case 'webhooks':
        flags.push('Outbound webhooks')
        break
      case 'embeddable_widget':
        flags.push('Embeddable storefront widget')
        break
      case 'support_priority':
        flags.push('Priority support')
        break
    }
  }
  return [...flags, ...limits]
}
