import { Outlet } from 'react-router-dom'
import { PageSpinner } from '@/components/ui/Spinner'
import { useAuthState } from '@/stores/auth'

/**
 * Guarded shell for authenticated users (PRD §6.3). If unauthenticated the
 * router redirects before rendering (see App.tsx); this renders the shell.
 */
export function UserShell() {
  const { user, loading } = useAuthState((s) => ({ user: s.user, loading: s.loading }))
  if (loading) return <PageSpinner label="Loading profile…" />
  if (!user) return <PageSpinner label="Signing you in…" />
  return (
    <div className="container-page py-10">
      <Outlet />
    </div>
  )
}
