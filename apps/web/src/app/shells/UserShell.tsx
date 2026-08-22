import { Outlet } from 'react-router-dom'
import { PageSpinner } from '@/components/ui/Spinner'
import { useAuth } from '@/stores/auth'

/**
 * Guarded shell for authenticated users (PRD §6.3). If unauthenticated the
 * router redirects before rendering (see App.tsx); this renders the shell.
 */
export function UserShell() {
  const { user, loading } = useAuth()
  if (loading) return <PageSpinner label="Loading profile…" />
  if (!user) return <PageSpinner label="Signing you in…" />
  return (
    <div className="container-page py-10">
      <Outlet />
    </div>
  )
}
