import { NavLink, Outlet } from 'react-router-dom'
import { PageSpinner } from '@/components/ui/Spinner'
import { MobileNav } from '@/components/ui/MobileNav'
import { useAuth } from '@/stores/auth'

const links = [
  { to: '/admin', label: 'Overview', end: true },
  { to: '/admin/verify', label: 'Verification' },
  { to: '/admin/moderation', label: 'Moderation' },
  { to: '/admin/categories', label: 'Categories' },
  { to: '/admin/users', label: 'Users' },
  { to: '/admin/curation', label: 'Curation' },
  { to: '/admin/appeals', label: 'Appeals & anomalies' },
  { to: '/admin/audit', label: 'Audit trail' },
  { to: '/admin/analytics', label: 'Analytics' },
]

/** Admin shell (PRD §6.5). Landed for real in M6. */
export function AdminShell() {
  const { user, loading } = useAuth()
  if (loading) return <PageSpinner label="Loading admin…" />
  if (!user) return <PageSpinner label="Signing you in…" />

  return (
    <div className="flex min-h-screen">
      <aside className="hidden w-56 shrink-0 border-r border-border px-4 py-6 md:block">
        <p className="mono-label mb-4">Admin</p>
        <nav className="space-y-1" aria-label="Admin">
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
          <p className="text-sm font-semibold text-ink">Admin</p>
          <MobileNav links={links} />
        </div>
        <main className="px-4 py-8 sm:px-8">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
