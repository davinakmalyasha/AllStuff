import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { PageSpinner } from '@/components/ui/Spinner'
import { api, type BusinessDTO } from '@/lib/api'
import { MobileNav } from '@/components/ui/MobileNav'
import { useAuthState } from '@/stores/auth'
import { useBusiness } from '@/stores/business'

const links = [
  { to: '/dashboard', label: 'Overview', end: true },
  { to: '/dashboard/register', label: 'New business' },
  { to: '/dashboard/storefront', label: 'Storefront' },
  { to: '/dashboard/products', label: 'Products' },
  { to: '/dashboard/chats', label: 'Chats' },
  { to: '/dashboard/analytics', label: 'Analytics' },
  { to: '/dashboard/reviews', label: 'Reviews' },
  { to: '/dashboard/comments', label: 'Comments' },
  { to: '/dashboard/billing', label: 'Billing' },
  { to: '/dashboard/settings', label: 'Settings' },
]

/** Owner dashboard shell (PRD Â§6.4) with the universal business switcher. */
export function OwnerShell() {
  const { user, loading } = useAuthState((s) => ({ user: s.user, loading: s.loading }))
  const navigate = useNavigate()
  const { selectedId, setSelected } = useBusiness()

  const { data } = useQuery({
    queryKey: ['my-businesses'],
    queryFn: () => api<{ businesses: BusinessDTO[] }>('/businesses'),
    enabled: !!user,
  })
  const businesses = data?.businesses ?? []
  const active = businesses.find((b) => b.id === selectedId) ?? businesses[0]

  if (loading) return <PageSpinner label="Loading dashboardâ€¦" />
  if (!user) return <PageSpinner label="Signing you inâ€¦" />

  return (
    <div className="flex min-h-screen">
      <aside className="hidden w-60 shrink-0 border-r border-border px-4 py-6 md:block">
        <p className="mono-label mb-2">Business</p>
        {businesses.length > 0 ? (
          <select
            value={active?.id ?? ''}
            onChange={(e) => {
              setSelected(e.target.value || null)
              if (e.target.value) navigate('/dashboard')
            }}
            className="mb-4 h-9 w-full rounded-lg border border-border bg-surface px-2 text-sm text-ink"
            aria-label="Switch business"
          >
            {businesses.map((b) => (
              <option key={b.id} value={b.id}>{b.name}</option>
            ))}
          </select>
        ) : (
          <button
            onClick={() => navigate('/dashboard/register')}
            className="mb-4 flex w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-border py-2 text-xs text-ink2 hover:bg-surface2"
          >
            <Plus className="h-3 w-3" /> Register a business
          </button>
        )}
        <p className="mono-label mb-4">Dashboard</p>
        <nav className="space-y-1" aria-label="Dashboard">
          {links.map((l) => (
            <NavLink
              key={l.to}
              to={l.to}
              end={l.end}
              className={({ isActive }) =>
                `block rounded-lg px-3 py-2 text-sm transition-colors ${
                  isActive ? 'bg-accent text-accent-ink' : 'text-ink2 hover:bg-surface2 hover:text-ink'
                }`
              }
            >
              {l.label}
            </NavLink>
          ))}
        </nav>
      </aside>
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between border-b border-border px-4 py-3 md:hidden">
          <p className="text-sm font-semibold text-ink">{active?.name ?? 'Dashboard'}</p>
          <MobileNav links={links} />
        </div>
        <main className="px-4 py-8 sm:px-8">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

export function useActiveBusiness() {
  const { selectedId } = useBusiness()
  const { user } = useAuthState((s) => ({ user: s.user }))
  const { data } = useQuery({
    queryKey: ['my-businesses'],
    queryFn: () => api<{ businesses: BusinessDTO[] }>('/businesses'),
    enabled: !!user,
  })
  const businesses = data?.businesses ?? []
  return businesses.find((b) => b.id === selectedId) ?? businesses[0] ?? null
}
