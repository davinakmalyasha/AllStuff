import { useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useNavigate } from 'react-router-dom'
import { Compass, Globe, LayoutDashboard, Map, Store, TrendingUp, User } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Logo } from '@/components/ui/Logo'
import { ThemeToggle } from '@/components/ui/ThemeToggle'
import { NotificationsBell } from '@/components/ui/NotificationsBell'
import { AnnouncementBanner } from '@/components/ui/AnnouncementBanner'
import { CompareTray } from '@/components/compare/CompareTray'
import { Button } from '@/components/ui/Button'
import { useAuth } from '@/stores/auth'
import { setLanguage } from '@/lib/i18n'

export function PublicLayout() {
  const { t, i18n } = useTranslation()
  const { user, initialized } = useAuth()
  const navigate = useNavigate()
  const [scrolled, setScrolled] = useState(false)

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 8)
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  const navLink = ({ isActive }: { isActive: boolean }) =>
    `text-sm transition-colors ${isActive ? 'text-ink' : 'text-ink2 hover:text-ink'}`

  return (
    <div className="flex min-h-screen flex-col">
      <AnnouncementBanner />
      <header
        className={`sticky top-0 z-40 border-b border-border bg-bg/90 backdrop-blur transition-shadow ${
          scrolled ? 'shadow-card' : ''
        }`}
      >
        <div className="container-page flex h-16 items-center justify-between gap-4">
          <Logo />
          <nav className="hidden items-center gap-6 md:flex" aria-label="Primary">
            <NavLink to="/discover" className={navLink}>{t('nav.discover')}</NavLink>
            <NavLink to="/categories" className={navLink}>{t('nav.categories')}</NavLink>
            <NavLink to="/map" className={navLink}>{t('nav.map')}</NavLink>
            <NavLink to="/leaderboards" className={navLink}>Leaderboards</NavLink>
            <NavLink to="/for-business" className={navLink}>{t('nav.forBusiness')}</NavLink>
          </nav>
          <div className="flex items-center gap-2">
            <ThemeToggle />
            <label className="flex items-center gap-1 text-ink3 hover:text-ink" title="Language / Bahasa">
              <Globe className="h-4 w-4" />
              <select
                value={i18n.language.startsWith('id') ? 'id' : 'en'}
                onChange={(e) => setLanguage(e.target.value as 'en' | 'id')}
                className="h-8 cursor-pointer rounded border border-border bg-surface px-1 text-xs text-ink2 focus:outline-none"
                aria-label="Language"
              >
                <option value="en">EN</option>
                <option value="id">ID</option>
              </select>
            </label>
            {initialized && user ? (
              <>
                <NotificationsBell />
                {user.is_admin && (
                  <Button variant="secondary" size="sm" onClick={() => navigate('/admin')}>
                    <LayoutDashboard className="h-4 w-4" /> Admin
                  </Button>
                )}
                <Button variant="ghost" size="sm" onClick={() => navigate('/me')}>
                  <User className="h-4 w-4" /> {user.name.split(' ')[0]}
                </Button>
              </>
            ) : (
              <>
                <Button variant="ghost" size="sm" onClick={() => navigate('/login')}>
                  {t('nav.login')}
                </Button>
                <Button size="sm" onClick={() => navigate('/register')}>
                  {t('nav.register')}
                </Button>
              </>
            )}
          </div>
        </div>
      </header>

      <main className="flex-1">
        <Outlet />
        <CompareTray />
      </main>

      {/* Mobile bottom nav */}
      <nav className="fixed inset-x-0 bottom-0 z-40 flex border-t border-border bg-bg/95 backdrop-blur md:hidden" aria-label="Mobile">
        {(
          [
            ['/discover', 'Discover', Compass],
            ['/map', 'Map', Map],
            ['/leaderboards', 'Trends', TrendingUp],
            ['/for-business', 'Owners', Store],
          ] as [string, string, typeof Compass][]
        ).map(([to, label, Icon]) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              `flex flex-1 flex-col items-center gap-0.5 py-2 text-[10px] ${isActive ? 'text-ink' : 'text-ink3'}`
            }
          >
            <Icon className="h-4 w-4" />
            {label}
          </NavLink>
        ))}
      </nav>

      <PublicFooter />
    </div>
  )
}

function PublicFooter() {
  const { t } = useTranslation()
  const year = new Date().getFullYear()
  return (
    <footer className="border-t border-border">
      <div className="container-page py-12">
        <div className="grid gap-10 md:grid-cols-5">
          <div className="md:col-span-2">
            <Logo />
            <p className="mt-3 max-w-xs text-sm text-ink2">{t('landing.footerTagline')}</p>
          </div>
          <div>
            <p className="mono-label mb-3">Discover</p>
            <ul className="space-y-2 text-sm text-ink2">
              <li><Link to="/discover" className="hover:text-ink">Businesses</Link></li>
              <li><Link to="/categories" className="hover:text-ink">Categories</Link></li>
              <li><Link to="/map" className="hover:text-ink">Map</Link></li>
              <li><Link to="/compare" className="hover:text-ink">Compare</Link></li>
            </ul>
          </div>
          <div>
            <p className="mono-label mb-3">For business</p>
            <ul className="space-y-2 text-sm text-ink2">
              <li><Link to="/for-business" className="hover:text-ink">Storefronts</Link></li>
              <li><Link to="/register" className="hover:text-ink">Register</Link></li>
              <li><Link to="/login" className="hover:text-ink">{t('nav.login')}</Link></li>
            </ul>
          </div>
          <div>
            <p className="mono-label mb-3">Support</p>
            <ul className="space-y-2 text-sm text-ink2">
              <li><Link to="/help" className="hover:text-ink">Help center</Link></li>
              <li><Link to="/help/business" className="hover:text-ink">Owner guide</Link></li>
              <li><Link to="/contact" className="hover:text-ink">Contact us</Link></li>
              <li><Link to="/terms" className="hover:text-ink">Terms</Link></li>
              <li><Link to="/privacy" className="hover:text-ink">Privacy</Link></li>
            </ul>
          </div>
        </div>
        <div className="mt-10 flex flex-col items-start justify-between gap-2 border-t border-border pt-6 text-xs text-ink3 sm:flex-row">
          <p>© {year} BizVerse. Free forever.</p>
        </div>
      </div>
    </footer>
  )
}
