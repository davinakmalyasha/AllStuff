import { lazy, Suspense, useEffect, useState } from 'react'
import {
  createBrowserRouter,
  Link,
  Navigate,
  RouterProvider,
  useLocation,
} from 'react-router-dom'
import { QueryClientProvider, useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import '@/lib/i18n'
import { ThemeProvider } from '@/theme/ThemeProvider'
import { ws } from '@/lib/ws'
import { ErrorBoundary } from '@/components/ui/ErrorBoundary'
import { PublicLayout } from '@/components/layout/PublicLayout'
import { UserShell } from '@/app/shells/UserShell'
import { OwnerShell } from '@/app/shells/OwnerShell'
import { AdminShell } from '@/app/shells/AdminShell'
import { LandingPage } from '@/features/landing/LandingPage'
import { DiscoverPage } from '@/features/discover/DiscoverPage'
// Low-traffic routes (admin, auth, legal, help, dashboard subpages) load on
// demand: statically importing them put ~500kB in the entry chunk every
// visitor paid for.
const CategoriesPage = lazy(() => import('@/features/pages/StaticPages').then((m) => ({ default: m.CategoriesPage })))
const ForBusinessPage = lazy(() => import('@/features/pages/StaticPages').then((m) => ({ default: m.ForBusinessPage })))
const ClaimPage = lazy(() => import('@/features/pages/ClaimPage').then((m) => ({ default: m.ClaimPage })))
const CityPage = lazy(() => import('@/features/discover/CityPage').then((m) => ({ default: m.CityPage })))
const UserProfilePage = lazy(() => import('@/features/me/UserProfilePage').then((m) => ({ default: m.UserProfilePage })))
const FollowingFeedPage = lazy(() => import('@/features/me/FollowingFeedPage').then((m) => ({ default: m.FollowingFeedPage })))
const HelpPage = lazy(() => import('@/features/help/HelpPages').then((m) => ({ default: m.HelpPage })))
const BusinessHelpPage = lazy(() => import('@/features/help/HelpPages').then((m) => ({ default: m.BusinessHelpPage })))
const ContactPage = lazy(() => import('@/features/help/ContactPage').then((m) => ({ default: m.ContactPage })))
const TermsPage = lazy(() => import('@/features/pages/LegalPages').then((m) => ({ default: m.TermsPage })))
const PrivacyPage = lazy(() => import('@/features/pages/LegalPages').then((m) => ({ default: m.PrivacyPage })))
const AdminKPIPage = lazy(() => import('@/features/admin/AdminKPIPage').then((m) => ({ default: m.AdminKPIPage })))
const AdminAppealsPage = lazy(() => import('@/features/admin/AdminAppealsPage').then((m) => ({ default: m.AdminAppealsPage })))
const AdminAuditPage = lazy(() => import('@/features/admin/AdminAuditPage').then((m) => ({ default: m.AdminAuditPage })))
const AdminClaimsPage = lazy(() => import('@/features/admin/AdminClaimsPage').then((m) => ({ default: m.AdminClaimsPage })))
const LeaderboardsPage = lazy(() => import('@/features/discover/LeaderboardsPage').then((m) => ({ default: m.LeaderboardsPage })))
const PublicCollectionPage = lazy(() => import('@/features/me/PublicCollectionPage').then((m) => ({ default: m.PublicCollectionPage })))
const MapPage = lazy(() => import('@/features/map/MapPage').then((m) => ({ default: m.MapPage })))
const ComparePage = lazy(() => import('@/features/compare/ComparePage').then((m) => ({ default: m.ComparePage })))
const LoginPage = lazy(() => import('@/features/auth/LoginPage').then((m) => ({ default: m.LoginPage })))
const RegisterPage = lazy(() => import('@/features/auth/RegisterPage').then((m) => ({ default: m.RegisterPage })))
const VerifyEmailPage = lazy(() => import('@/features/auth/VerifyEmailPage').then((m) => ({ default: m.VerifyEmailPage })))
const ForgotPasswordPage = lazy(() => import('@/features/auth/ForgotPasswordPage').then((m) => ({ default: m.ForgotPasswordPage })))
const ResetPasswordPage = lazy(() => import('@/features/auth/ResetPasswordPage').then((m) => ({ default: m.ResetPasswordPage })))
const RestorePage = lazy(() => import('@/features/auth/RestorePage').then((m) => ({ default: m.RestorePage })))
const InviteAcceptPage = lazy(() => import('@/features/pages/InviteAcceptPage').then((m) => ({ default: m.InviteAcceptPage })))
const MePage = lazy(() => import('@/features/me/MePage').then((m) => ({ default: m.MePage })))
const CollectionsPage = lazy(() => import('@/features/me/CollectionsPage').then((m) => ({ default: m.CollectionsPage })))
const NotificationsPage = lazy(() => import('@/features/me/NotificationsPage').then((m) => ({ default: m.NotificationsPage })))
const MyReviewsPage = lazy(() => import('@/features/me/ActivityPages').then((m) => ({ default: m.MyReviewsPage })))
const ExportPage = lazy(() => import('@/features/me/ActivityPages').then((m) => ({ default: m.ExportPage })))
const SecurityPage = lazy(() => import('@/features/me/SecurityPage').then((m) => ({ default: m.SecurityPage })))
const InboxPage = lazy(() => import('@/features/chat/InboxPage').then((m) => ({ default: m.InboxPage })))
const ThreadPage = lazy(() => import('@/features/chat/ThreadPage').then((m) => ({ default: m.ThreadPage })))
const DashboardChatsPage = lazy(() => import('@/features/chat/DashboardChatsPage').then((m) => ({ default: m.DashboardChatsPage })))
const BusinessPage = lazy(() => import('@/features/business/BusinessPage').then((m) => ({ default: m.BusinessPage })))
const CategoryPage = lazy(() => import('@/features/discover/CategoryPage').then((m) => ({ default: m.CategoryPage })))
const BusinessWizardPage = lazy(() => import('@/features/dashboard/BusinessWizardPage').then((m) => ({ default: m.BusinessWizardPage })))
const VerificationSettingsPage = lazy(() => import('@/features/dashboard/VerificationSettingsPage').then((m) => ({ default: m.VerificationSettingsPage })))
const StorefrontBuilderPage = lazy(() => import('@/features/dashboard/StorefrontBuilderPage').then((m) => ({ default: m.StorefrontBuilderPage })))
const ProductsPage = lazy(() => import('@/features/dashboard/ProductsPage').then((m) => ({ default: m.ProductsPage })))
const SettingsPage = lazy(() => import('@/features/dashboard/SettingsPage').then((m) => ({ default: m.SettingsPage })))
const AnalyticsPage = lazy(() => import('@/features/dashboard/AnalyticsPage').then((m) => ({ default: m.AnalyticsPage })))
const BillingPage = lazy(() => import('@/features/dashboard/BillingPage').then((m) => ({ default: m.BillingPage })))
const PricingPage = lazy(() => import('@/features/pages/PricingPage').then((m) => ({ default: m.PricingPage })))
const DashboardReviewsPage = lazy(() => import('@/features/dashboard/EngagementPages').then((m) => ({ default: m.DashboardReviewsPage })))
const DashboardCommentsPage = lazy(() => import('@/features/dashboard/EngagementPages').then((m) => ({ default: m.DashboardCommentsPage })))
const AdminSettingsPage = lazy(() => import('@/features/admin/AdminSettingsPage').then((m) => ({ default: m.AdminSettingsPage })))
const AdminCategoriesPage = lazy(() => import('@/features/admin/AdminCategoriesPage').then((m) => ({ default: m.AdminCategoriesPage })))
const AdminVerifyPage = lazy(() => import('@/features/admin/AdminVerifyPage').then((m) => ({ default: m.AdminVerifyPage })))
const AdminModerationPage = lazy(() => import('@/features/admin/AdminModerationPage').then((m) => ({ default: m.AdminModerationPage })))
const AdminUsersPage = lazy(() => import('@/features/admin/AdminUsersPage').then((m) => ({ default: m.AdminUsersPage })))
const AdminCurationPage = lazy(() => import('@/features/admin/AdminCurationPage').then((m) => ({ default: m.AdminCurationPage })))
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner } from '@/components/ui/Spinner'
import { CurrencyProvider } from '@/components/CurrencyProvider'
import { ToastStack, toast } from '@/components/ui/Toast'
import { api, type BusinessDTO } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { useAuthState } from '@/stores/auth'
import { hydrateCompare } from '@/stores/compare'

function ScrollToTop() {
  const { pathname } = useLocation()
  useEffect(() => {
    window.scrollTo(0, 0)
  }, [pathname])
  return null
}

/** Suspense boundary for lazy routes. */
function Lazy({ children }: { children: React.ReactNode }) {
  // The boundary is INSIDE the Suspense fallback's sibling, so a lazy chunk
  // that resolves but throws on render still shows a recoverable panel rather
  // than a blank page. `key` on the pathname resets the error state when the
  // user navigates away, so one broken route does not poison the next one.
  const { pathname } = useLocation()
  return (
    <ErrorBoundary key={pathname} label={pathname}>
      <Suspense fallback={<PageSpinner />}>{children}</Suspense>
    </ErrorBoundary>
  )
}

function GuestOnly({ children }: { children: React.ReactNode }) {
  const { user, initialized } = useAuthState((s) => ({ user: s.user, initialized: s.initialized }))
  if (initialized && user) return <Navigate to="/me" replace />
  return <>{children}</>
}

function RequireAuth({ children }: { children: React.ReactNode }) {
  const { user, initialized } = useAuthState((s) => ({ user: s.user, initialized: s.initialized }))
  const location = useLocation()
  if (!initialized) return null // brief; fetchMe resolves fast
  if (!user) return <Navigate to={`/login?next=${encodeURIComponent(location.pathname)}`} replace />
  return <>{children}</>
}

function RequireAdmin({ children }: { children: React.ReactNode }) {
  const { user, initialized } = useAuthState((s) => ({ user: s.user, initialized: s.initialized }))
  // Check initialized too: without it this guard bounces admins to "/" while
  // booting whenever it's ever mounted outside RequireAuth after a refactor.
  if (!initialized || !user?.is_admin) return <Navigate to="/" replace />
  return <>{children}</>
}

function NotFoundPage() {
  return (
    <div className="container-page flex min-h-[50vh] flex-col items-center justify-center gap-3 text-center">
      <p className="font-mono text-5xl font-semibold tracking-tight">404</p>
      <p className="text-sm text-ink2">This page doesn't exist or may have moved.</p>
      <div className="mt-2 flex gap-2">
        <Link to="/"><Button variant="secondary" size="sm">Home</Button></Link>
        <Link to="/discover"><Button variant="secondary" size="sm">Discover</Button></Link>
      </div>
    </div>
  )
}

const router = createBrowserRouter([
  {
    path: '/',
    element: (
      <>
        <ScrollToTop />
        <PublicLayout />
      </>
    ),
    children: [
      { index: true, element: <LandingPage /> },
      { path: 'discover', element: <DiscoverPage /> },
      { path: 'leaderboards', element: <Lazy><LeaderboardsPage /></Lazy> },
      { path: 'categories', element: <Lazy><CategoriesPage /></Lazy> },
      { path: 'map', element: <Lazy><MapPage /></Lazy> },
      { path: 'compare', element: <Lazy><ComparePage /></Lazy> },
      { path: 'for-business', element: <Lazy><ForBusinessPage /></Lazy> },
      { path: 'pricing', element: <Lazy><PricingPage /></Lazy> },
      { path: 'claim', element: <Lazy><ClaimPage /></Lazy> },
      { path: 'b/:slug', element: <Lazy><BusinessPage /></Lazy> },
      { path: 'c/:slug', element: <Lazy><CategoryPage /></Lazy> },
      { path: 'city/:slug', element: <Lazy><CityPage /></Lazy> },
      { path: 'collections/:id', element: <Lazy><PublicCollectionPage /></Lazy> },
      { path: 'u/:username', element: <Lazy><UserProfilePage /></Lazy> },
      { path: 'help', element: <Lazy><HelpPage /></Lazy> },
      { path: 'help/business', element: <Lazy><BusinessHelpPage /></Lazy> },
      { path: 'contact', element: <Lazy><ContactPage /></Lazy> },
      { path: 'terms', element: <Lazy><TermsPage /></Lazy> },
      { path: 'privacy', element: <Lazy><PrivacyPage /></Lazy> },
      {
        path: 'login',
        element: (
          <GuestOnly>
            <Lazy><LoginPage /></Lazy>
          </GuestOnly>
        ),
      },
      {
        path: 'register',
        element: (
          <GuestOnly>
            <Lazy><RegisterPage /></Lazy>
          </GuestOnly>
        ),
      },
      {
        path: 'auth/restore',
        element: (
          <GuestOnly>
            <Lazy><RestorePage /></Lazy>
          </GuestOnly>
        ),
      },
      { path: 'invite/:token', element: <Lazy><InviteAcceptPage /></Lazy> },
      { path: 'verify-email', element: <Lazy><VerifyEmailPage /></Lazy> },
      { path: 'forgot-password', element: <Lazy><ForgotPasswordPage /></Lazy> },
      { path: 'reset-password', element: <Lazy><ResetPasswordPage /></Lazy> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
  {
    path: '/me',
    element: (
      <RequireAuth>
        <UserShell />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Lazy><MePage /></Lazy> },
      { path: 'collections', element: <Lazy><CollectionsPage /></Lazy> },
      { path: 'security', element: <Lazy><SecurityPage /></Lazy> },
      { path: 'notifications', element: <Lazy><NotificationsPage /></Lazy> },
      { path: 'reviews', element: <Lazy><MyReviewsPage /></Lazy> },
      { path: 'export', element: <Lazy><ExportPage /></Lazy> },
      { path: 'following', element: <Lazy><FollowingFeedPage /></Lazy> },
      { path: 'messages', element: <Lazy><InboxPage /></Lazy> },
      { path: 'messages/:id', element: <Lazy><ThreadPage /></Lazy> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
  {
    path: '/dashboard',
    element: (
      <RequireAuth>
        <OwnerShell />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <DashboardIndex /> },
      { path: 'register', element: <Lazy><BusinessWizardPage /></Lazy> },
      { path: 'storefront', element: <Lazy><StorefrontBuilderPage /></Lazy> },
      { path: 'products', element: <Lazy><ProductsPage /></Lazy> },
      { path: 'chats', element: <Lazy><DashboardChatsPage /></Lazy> },
      { path: 'chats/:id', element: <Lazy><ThreadPage businessMode /></Lazy> },
      { path: 'settings', element: <Lazy><SettingsPage /></Lazy> },
      { path: 'settings/verification', element: <Lazy><VerificationSettingsPage /></Lazy> },
      { path: 'analytics', element: <Lazy><AnalyticsPage /></Lazy> },
      { path: 'billing', element: <Lazy><BillingPage /></Lazy> },
      { path: 'reviews', element: <Lazy><DashboardReviewsPage /></Lazy> },
      { path: 'comments', element: <Lazy><DashboardCommentsPage /></Lazy> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
  {
    path: '/admin',
    element: (
      <RequireAuth>
        <RequireAdmin>
          <AdminShell />
        </RequireAdmin>
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Lazy><AdminKPIPage /></Lazy> },
      { path: 'analytics', element: <Lazy><AdminKPIPage /></Lazy> },
      { path: 'categories', element: <Lazy><AdminCategoriesPage /></Lazy> },
      { path: 'verify', element: <Lazy><AdminVerifyPage /></Lazy> },
      { path: 'moderation', element: <Lazy><AdminModerationPage /></Lazy> },
      { path: 'users', element: <Lazy><AdminUsersPage /></Lazy> },
      { path: 'curation', element: <Lazy><AdminCurationPage /></Lazy> },
      { path: 'appeals', element: <Lazy><AdminAppealsPage /></Lazy> },
      { path: 'audit', element: <Lazy><AdminAuditPage /></Lazy> },
      { path: 'claims', element: <Lazy><AdminClaimsPage /></Lazy> },
      { path: 'settings', element: <Lazy><AdminSettingsPage /></Lazy> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
])

function DashboardIndex() {
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
          <Button size="sm"><Plus className="h-4 w-4" /> New business</Button>
        </Link>
      </div>
      {businesses.length === 0 ? (
        <Card className="py-12 text-center">
          <p className="text-sm text-ink2">No businesses yet. Register your first storefront â€” it takes about 10 minutes and it's free forever.</p>
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
                <div className="flex h-11 w-11 items-center justify-center rounded-lg bg-surface2 text-base font-semibold">{b.name.charAt(0)}</div>
              )}
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-semibold text-ink">{b.name}</p>
                <p className="text-xs text-ink3">/{b.slug}</p>
              </div>
              <Badge tone={STATUS_TONE[b.status] ?? 'neutral'} dot>{STATUS_LABEL[b.status] ?? b.status}</Badge>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}

const STATUS_LABEL: Record<string, string> = {
  draft: 'Draft', pending_review: 'Pending review', verified: 'Verified',
  rejected: 'Rejected', suspended: 'Suspended', paused: 'Paused', closed: 'Closed',
}
const STATUS_TONE: Record<string, 'neutral' | 'attention' | 'positive' | 'danger'> = {
  draft: 'neutral', pending_review: 'attention', verified: 'positive', rejected: 'danger',
  suspended: 'danger', paused: 'neutral', closed: 'neutral',
}

function Bootstrap() {
  const { fetchMe, user } = useAuthState((s) => ({ fetchMe: s.fetchMe, user: s.user }))
  useEffect(() => {
    void fetchMe()
    if ('serviceWorker' in navigator) {
      navigator.serviceWorker
        .register('/sw.js')
        .then((reg) => {
          // Update flow: when a waiting SW activates while a controller is
          // already driving the page, tell the user a refresh is available.
          reg.addEventListener('updatefound', () => {
            const installing = reg.installing
            if (!installing) return
            installing.addEventListener('statechange', () => {
              if (installing.state === 'activated' && navigator.serviceWorker.controller) {
                toast.info('A new version is available â€” refresh to update.')
              }
            })
          })
        })
        .catch(() => undefined)
    }
    const orig = window.onerror
    window.onerror = (msg, src, line, col, err) => {
      try {
        void fetch('/api/v1/errors', {
          method: 'POST',
          credentials: 'include',
          headers: { 'Content-Type': 'application/json' },
          // Strip query + hash: URLs here carry ?token= recovery secrets that
          // must not land in server-side error logs.
          body: JSON.stringify({ message: String(msg), stack: err?.stack ?? `${src}:${line}:${col}`, url: window.location.origin + window.location.pathname }),
        })
      } catch {
        /* noop */
      }
      return orig?.(msg, src, line, col, err) ?? false
    }
    // Restore the previous handler on unmount (StrictMode remounts would
    // otherwise stack reporters that never go away).
    return () => {
      window.onerror = orig
    }
  }, [fetchMe])
  // Compare tray follows the account across devices (PRD Â§5.1.5).
  useEffect(() => {
    if (user) void hydrateCompare()
  }, [user])
  return (
    <>
      <CurrencyProvider />
      <ToastStack />
      <TabTitle />
      <KeyboardShortcuts />
      <BackToTop />
      <LiveEvents />
      <RouterProvider router={router} />
    </>
  )
}

/** Keeps one WS connection open for authed users: chat + notification.new.
 * Disconnects on logout â€” previously a zombie socket stayed connected (and
 * reconnecting forever) after the session ended. */
function LiveEvents() {
  const { user } = useAuthState((s) => ({ user: s.user }))
  // Key on the stable id, not the object: fetchMe() after avatar uploads /
  // verifications produced a fresh user object and needlessly bounced the
  // socket (dropping every thread subscription mid-session).
  const userId = user?.id ?? null
  useEffect(() => {
    if (!userId) return
    ws.connect()
    return () => ws.close()
  }, [userId])
  return null
}

function TabTitle() {
  const { user } = useAuthState((s) => ({ user: s.user }))
  const { data: notif } = useQuery({
    // Distinct key: sharing ['notifications'] with NotificationsBell (which
    // fetches limit=15) made two queryFns fight over one cache entry, so the
    // dropdown flickered down to a single notification on alternate polls.
    queryKey: ['notifications', 'count'],
    queryFn: () => api<{ unread: number }>('/notifications?limit=1'),
    enabled: !!user,
    refetchInterval: 60_000,
  })
  const { data: threads } = useQuery({
    queryKey: ['threads', 'all'],
    queryFn: () => api<{ threads: Array<{ unread: number }> }>('/threads'),
    enabled: !!user,
    refetchInterval: 60_000,
  })
  useEffect(() => {
    const unread = (notif?.unread ?? 0) + (threads?.threads ?? []).reduce((a, t) => a + t.unread, 0)
    const base = document.title.replace(/^\(\d+\) /, '')
    // Always assign: the prefix must also CLEAR when unread hits 0
    // (previously "(3) BizVerse" stuck forever after catching up).
    document.title = unread > 0 ? `(${unread}) ${base}` : base
  }, [notif, threads])
  return null
}

function KeyboardShortcuts() {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = document.activeElement
      if (e.key === '/' && el?.tagName !== 'INPUT' && el?.tagName !== 'TEXTAREA') {
        e.preventDefault()
        document.querySelector<HTMLInputElement>('input[type="search"], input[placeholder*="Search" i]')?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  return null
}

function BackToTop() {
  const [show, setShow] = useState(false)
  useEffect(() => {
    const onScroll = () => setShow(window.scrollY > 600)
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])
  if (!show) return null
  return (
    <button
      onClick={() => window.scrollTo({ top: 0, behavior: 'smooth' })}
      className="fixed bottom-16 right-4 z-40 flex h-10 w-10 items-center justify-center rounded-full border border-border bg-surface text-ink shadow-cardHover hover:bg-surface2"
      aria-label="Back to top"
    >
      â†‘
    </button>
  )
}

export function App() {
  return (
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <Bootstrap />
      </QueryClientProvider>
    </ThemeProvider>
  )
}
