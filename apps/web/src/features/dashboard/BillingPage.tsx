import { useMutation, useQuery } from '@tanstack/react-query'
import { api, type BillingStateDTO, type InvoiceDTO, type PlanDTO } from '@/lib/api'
import { formatDate } from '@/lib/format'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Card } from '@/components/ui/Card'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner, ErrorNote } from '@/components/ui/Spinner'
import { toast } from '@/components/ui/Toast'

/**
 * Owner billing (Phase 7.1).
 *
 * Two things this surface deliberately does NOT do:
 *  - It does not trust the URL after checkout. Stripe redirects back here, but
 *    the authoritative state is `GET /businesses/{id}/billing`, so a return
 *    with `?checkout=success` while nothing actually changed still renders the
 *    truth (still Free) rather than a lie.
 *  - It does not call the Stripe API from the browser. The secret key never
 *    leaves the server; checkout is a server-created Session URL.
 */
export function BillingPage() {
  const business = useActiveBusiness()

  const state = useQuery({
    queryKey: ['billing', business?.id],
    queryFn: () => api<BillingStateDTO>(`/businesses/${business!.id}/billing`),
    enabled: !!business,
  })

  const invoices = useQuery({
    queryKey: ['billing-invoices', business?.id],
    queryFn: () => api<{ invoices: InvoiceDTO[] }>(`/businesses/${business!.id}/billing/invoices`),
    enabled: !!business,
  })

  // After checkout or portal, re-read the server state rather than guessing
  // from the redirect query string.
  const checkout = useMutation({
    mutationFn: (planId: string) =>
      api<{ url: string }>(`/businesses/${business!.id}/billing/checkout`, {
        method: 'POST',
        body: { plan_id: planId },
      }),
    onSuccess: (res) => {
      if (res.url) window.location.href = res.url
    },
    onError: (e: Error) => toast.error(e.message),
  })

  const portal = useMutation({
    mutationFn: () => api<{ url: string }>(`/businesses/${business!.id}/billing/portal`, { method: 'POST' }),
    onSuccess: (res) => {
      if (res.url) window.location.href = res.url
    },
    onError: (e: Error) => toast.error(e.message),
  })

  if (!business) {
    return (
      <div className="mx-auto max-w-xl">
        <h1 className="text-2xl font-semibold tracking-tight">No business selected</h1>
      </div>
    )
  }
  if (state.isLoading) return <PageSpinner />
  if (state.isError) {
    return (
      <div className="mx-auto max-w-4xl py-10">
        <ErrorNote message="Billing details are unavailable right now." onRetry={() => void state.refetch()} />
      </div>
    )
  }

  const sub = state.data?.subscription
  const plan = state.data?.plan
  const ent = state.data?.entitlements
  const enabled = state.data?.enabled ?? false

  return (
    <div className="mx-auto max-w-4xl">
      <p className="mono-label mb-1">Billing</p>
      <h1 className="mb-6 text-2xl font-semibold tracking-tight">{business.name}</h1>

      {/* ---- current plan ---- */}
      <Card className="mb-5">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <div className="flex items-center gap-2">
              <h2 className="text-lg font-semibold">{plan?.name ?? 'Free'}</h2>
              {sub && <SubscriptionBadge status={sub.status} />}
            </div>
            <p className="mt-1 text-sm text-ink2">
              {sub?.price_cents
                ? `${(sub.price_cents / 100).toFixed(2)} ${sub.currency} / month`
                : 'Free forever'}
            </p>
            {sub?.cancel_at_period_end && sub.current_period_end && (
              <p className="mt-2 text-sm text-ink2">
                Cancels on {formatDate(sub.current_period_end)}. You keep access until then.
              </p>
            )}
            {sub?.current_period_end && !sub.cancel_at_period_end && sub.status === 'active' && (
              <p className="mt-2 text-sm text-ink2">Renews {formatDate(sub.current_period_end)}.</p>
            )}
          </div>
          {sub && (
            <Button
              variant="secondary"
              size="sm"
              disabled={!enabled || portal.isPending}
              onClick={() => portal.mutate()}
            >
              {portal.isPending ? 'Opening…' : 'Manage billing'}
            </Button>
          )}
        </div>

        {!enabled && (
          <p className="mt-4 rounded-lg border border-border bg-surface2 px-3 py-2 text-xs text-ink2">
            Payments are not configured on this deployment. You are on the free plan; upgrade options are
            unavailable.
          </p>
        )}
      </Card>

      {/* ---- capabilities ---- */}
      {ent && (
        <Card className="mb-5">
          <h3 className="mb-3 text-sm font-semibold">What your plan includes</h3>
          <CapabilityGrid entitlements={ent} />
        </Card>
      )}

      {/* ---- upgrade ---- */}
      <PlanPicker
        currentPlanId={plan?.id ?? 'free'}
        enabled={enabled}
        pending={checkout.isPending}
        onSelect={(id) => checkout.mutate(id)}
      />

      {/* ---- history ---- */}
      {invoices.data && invoices.data.invoices.length > 0 && (
        <Card className="mt-5">
          <h3 className="mb-3 text-sm font-semibold">Invoice history</h3>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <caption className="sr-only">Past invoices</caption>
              <thead>
                <tr className="border-b border-border text-left text-xs text-ink2">
                  <th scope="col" className="py-2 pr-3 font-medium">Invoice</th>
                  <th scope="col" className="py-2 pr-3 font-medium">Date</th>
                  <th scope="col" className="py-2 pr-3 font-medium">Amount</th>
                  <th scope="col" className="py-2 pr-3 font-medium">Status</th>
                  <th scope="col" className="py-2 font-medium">
                    <span className="sr-only">Download</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {invoices.data.invoices.map((inv) => (
                  <tr key={inv.id} className="border-b border-border last:border-0">
                    <td className="py-2 pr-3 font-mono text-xs">{inv.number ?? inv.id.slice(0, 12)}</td>
                    <td className="py-2 pr-3 text-ink2">{formatDate(inv.created_at)}</td>
                    <td className="py-2 pr-3">
                      {(inv.amount_cents / 100).toFixed(2)} {inv.currency}
                    </td>
                    <td className="py-2 pr-3">
                      <Badge tone={inv.status === 'paid' ? 'positive' : 'neutral'}>{inv.status ?? 'unknown'}</Badge>
                    </td>
                    <td className="py-2 text-right">
                      {inv.hosted_invoice_url && (
                        <a
                          href={inv.hosted_invoice_url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-xs text-ink2 underline hover:text-ink"
                        >
                          View
                        </a>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
    </div>
  )
}

/**
 * Status pill. A past_due subscription still grants access (the server keeps a
 * grace period while Stripe retries), so it must not read as "cancelled".
 */
function SubscriptionBadge({ status }: { status: string }) {
  const tone = status === 'active' || status === 'trialing' ? 'positive'
    : status === 'past_due' ? 'attention'
    : status === 'canceled' || status === 'unpaid' ? 'danger'
    : 'neutral'
  return <Badge tone={tone}>{status.replace(/_/g, ' ')}</Badge>
}

/**
 * What the plan currently buys.
 *
 * Capacity only. Verification and featured placement are deliberately absent and
 * are not "removed features" — they are not purchasable at any price, so
 * listing them here as an empty row would be a lie about the product.
 */
function CapabilityGrid({ entitlements }: { entitlements: NonNullable<BillingStateDTO['entitlements']> }) {
  const rows: { label: string; value: string }[] = [
    { label: 'Published products', value: `${entitlements.product_limit}` },
    { label: 'Storefront gallery photos', value: `${entitlements.gallery_limit}` },
    { label: 'Team seats', value: `${entitlements.team_seats}` },
    { label: 'Analytics', value: entitlements.analytics ? 'Included' : '—' },
    { label: 'Advanced analytics (funnels, response time)', value: entitlements.analytics_advanced ? 'Included' : '—' },
    { label: 'Public read API keys', value: entitlements.api_access ? 'Included' : '—' },
    { label: 'Priority support', value: entitlements.support_priority ? 'Included' : '—' },
  ]
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-1.5 sm:grid-cols-2">
      {rows.map((r) => (
        <div key={r.label} className="flex items-baseline justify-between gap-3 border-b border-border py-1.5 last:border-0">
          <dt className="text-sm text-ink2">{r.label}</dt>
          <dd className={`text-sm ${r.value === '—' ? 'text-ink3' : 'font-medium'}`}>{r.value}</dd>
        </div>
      ))}
    </dl>
  )
}

function PlanPicker({
  currentPlanId,
  enabled,
  pending,
  onSelect,
}: {
  currentPlanId: string
  enabled: boolean
  pending: boolean
  onSelect: (planId: string) => void
}) {
  const plans = useQuery({
    queryKey: ['plans'],
    queryFn: () => api<{ plans: PlanDTO[] }>('/plans'),
    staleTime: 5 * 60 * 1000,
  })

  if (plans.isLoading) return <PageSpinner label="Loading plans…" />
  if (plans.isError) {
    return (
      <Card>
        <ErrorNote message="Plans are unavailable right now." onRetry={() => void plans.refetch()} />
      </Card>
    )
  }

  const list = plans.data?.plans ?? []
  const paid = list.filter((p) => p.price_cents > 0)
  if (paid.length === 0) return null

  return (
    <section aria-labelledby="plans-heading" className="mt-5">
      <h2 id="plans-heading" className="mb-3 text-sm font-semibold">Upgrade</h2>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        {paid.map((p) => {
          const isCurrent = p.id === currentPlanId
          return (
            <Card key={p.id} className={p.id === 'pro' ? 'border-ink' : ''}>
              <div className="flex items-baseline justify-between gap-2">
                <h3 className="font-semibold">{p.name}</h3>
                {p.id === 'pro' && <Badge>Recommended</Badge>}
              </div>
              <p className="mt-1 text-2xl font-semibold tracking-tight">
                {(p.price_cents / 100).toFixed(0)}
                <span className="ml-1 text-sm font-normal text-ink2">{p.currency} / month</span>
              </p>
              <p className="mt-2 text-sm text-ink2">{p.description}</p>
              <Button
                className="mt-4"
                fullWidth
                disabled={isCurrent || !p.purchasable || !enabled || pending}
                onClick={() => onSelect(p.id)}
              >
                {isCurrent ? 'Current plan' : !p.purchasable ? 'Not available yet' : 'Choose ' + p.name}
              </Button>
            </Card>
          )
        })}
      </div>
    </section>
  )
}
