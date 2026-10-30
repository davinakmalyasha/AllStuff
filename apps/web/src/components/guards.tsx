"use client"

// Route guards, ported verbatim from the Vite App.tsx. The proxy.ts UX gate
// (cookie presence) runs first at the edge; these are the client-side
// behavior layer: post-login redirection, ?next= preservation, and the admin
// role check. The API remains the real authority on every call.
import { Navigate, useLocation } from 'react-router-dom'
import { useAuthState } from '@/stores/auth'

export function GuestOnly({ children }: { children: React.ReactNode }) {
  const { user, initialized } = useAuthState((s) => ({ user: s.user, initialized: s.initialized }))
  if (initialized && user) return <Navigate to="/me" replace />
  return <>{children}</>
}

export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, initialized } = useAuthState((s) => ({ user: s.user, initialized: s.initialized }))
  const location = useLocation()
  if (!initialized) return null // brief; fetchMe resolves fast
  if (!user) return <Navigate to={`/login?next=${encodeURIComponent(location.pathname)}`} replace />
  return <>{children}</>
}

export function RequireAdmin({ children }: { children: React.ReactNode }) {
  const { user, initialized } = useAuthState((s) => ({ user: s.user, initialized: s.initialized }))
  // Check initialized too: without it this guard bounces admins to "/" while
  // booting whenever it's ever mounted outside RequireAuth after a refactor.
  if (!initialized || !user?.is_admin) return <Navigate to="/" replace />
  return <>{children}</>
}
