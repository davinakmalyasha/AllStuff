import { Link } from 'react-router-dom'
import { Card } from '@/components/ui/Card'

export function CategoriesPage() {
  return (
    <div className="container-page py-10">
      <p className="mono-label mb-2">Directory</p>
      <h1 className="text-2xl font-semibold tracking-tight">Categories</h1>
      <p className="mt-1 text-sm text-ink2">
        Browse the directory by category: open <Link to="/discover" className="underline underline-offset-4 hover:text-ink">Discover</Link> or the{' '}
        <Link to="/map" className="underline underline-offset-4 hover:text-ink">Map</Link>, tap a category chip, and land on that category's page
        at <span className="font-mono text-xs">/c/:slug</span> — filtered listings, sub-categories included.
      </p>
      <Card className="mt-8 flex flex-col items-center justify-center gap-2 py-16 text-center">
        <p className="font-mono text-sm text-ink3">Live · categories</p>
        <p className="max-w-md text-sm text-ink2">
          Every business belongs to one admin-managed tree. Category pages are reachable from search, map chips, and every business profile.
        </p>
      </Card>
    </div>
  )
}

export function ForBusinessPage() {
  return (
    <div className="container-page py-10">
      <p className="mono-label mb-2">For business</p>
      <h1 className="text-2xl font-semibold tracking-tight">For business</h1>
      <p className="mt-1 text-sm text-ink2">
        Everything an owner needs is shipped: register through the wizard, style your storefront, manage a catalog, and chat with customers — all
        free from your dashboard.
      </p>
      <Card className="mt-8 grid gap-4 py-10 text-left sm:grid-cols-2 sm:text-center">
        <div>
          <p className="font-mono text-sm text-ink3">Register & verify</p>
          <p className="mt-1 text-sm text-ink2">
            The <Link to="/dashboard/register" className="underline underline-offset-4 hover:text-ink">registration wizard</Link> takes ~10 minutes;
            verification and trust levels live in{' '}
            <Link to="/dashboard/settings/verification" className="underline underline-offset-4 hover:text-ink">settings</Link>.
          </p>
        </div>
        <div>
          <p className="font-mono text-sm text-ink3">Storefront & catalog</p>
          <p className="mt-1 text-sm text-ink2">
            Theme your page in the <Link to="/dashboard/storefront" className="underline underline-offset-4 hover:text-ink">storefront builder</Link>{' '}
            and list products/services under <Link to="/dashboard/products" className="underline underline-offset-4 hover:text-ink">Products</Link>.
          </p>
        </div>
        <div>
          <p className="font-mono text-sm text-ink3">Messaging & reviews</p>
          <p className="mt-1 text-sm text-ink2">
            Answer customers in <Link to="/dashboard/chats" className="underline underline-offset-4 hover:text-ink">chats</Link> and reply to reviews
            and comments from your dashboard inbox.
          </p>
        </div>
        <div>
          <p className="font-mono text-sm text-ink3">Analytics</p>
          <p className="mt-1 text-sm text-ink2">
            Views, likes, top products, and leaderboard rank are on your{' '}
            <Link to="/dashboard/analytics" className="underline underline-offset-4 hover:text-ink">analytics page</Link>.
          </p>
        </div>
      </Card>
    </div>
  )
}
