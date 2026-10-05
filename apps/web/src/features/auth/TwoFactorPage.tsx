import { useEffect, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { AuthShell, AuthFooterLink } from './AuthShell'
import { TwoFactorForm } from './TwoFactorForm'
import { safeInternalPath } from '@/lib/url'
import { useAuth, useAuthState } from '@/stores/auth'

/**
 * Landing page for a second factor, reached by the OAuth callback.
 *
 * The API redirects here as `/2fa#challenge=<token>` when a Google login
 * succeeds for an account with TOTP enrolled.
 *
 * WHY THE CHALLENGE IS IN THE FRAGMENT
 * ------------------------------------
 * A fragment is not sent to the server. A query string is: it lands in the
 * access log, in any proxy's log, in browser history, and in the Referer header
 * on the next cross-origin navigation. The challenge is a bearer token for the
 * second factor - anyone holding it plus a valid code gets a session - so it is
 * carried the one way that does not write it down.
 *
 * This is the same reasoning that removed the WebSocket JWT from the query
 * string.
 */
export function TwoFactorPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const verify2FA = useAuthState((s) => s.verify2FA)

  // `next` is optional and only used by the redirect, so it is read from the
  // query string while the challenge comes from the fragment. safeInternalPath
  // is the same guard /login uses, for the same reason.
  const { challenge, next } = useMemo(() => {
    const hash = window.location.hash.startsWith('#') ? window.location.hash.slice(1) : ''
    const hashParams = new URLSearchParams(hash)
    const search = new URLSearchParams(window.location.search)
    return {
      challenge: hashParams.get('challenge') ?? '',
      next: safeInternalPath(search.get('next'), '/me'),
    }
  }, [])

  // Seed the store so a reload mid-flow still has the challenge, then scrub the
  // fragment. Without the scrub the token stays in the address bar and in
  // history, which is the whole reason it travelled in a fragment.
  useEffect(() => {
    if (challenge) useAuth.getState().setTwoFaChallenge(challenge)
    window.history.replaceState(null, '', window.location.pathname + window.location.search)
  }, [challenge])

  const stored = useAuth((s) => s.twoFaChallenge)

  if (!stored) {
    // No challenge anywhere: the user arrived here directly, or the flow
    // expired. The 5-minute challenge TTL is short by design, so this is an
    // expected state rather than an error.
    return (
      <AuthShell
        title={t('auth.twoFaTitle')}
        subtitle={t('auth.twoFaExpired')}
        footer={<AuthFooterLink text={t('auth.noAccount')} to="/register" label={t('auth.createAccount')} />}
      >
        <div className="space-y-4">
          <p className="text-sm text-ink3">{t('auth.twoFaExpiredBody')}</p>
          <a
            href="/login"
            className="inline-flex h-10 w-full items-center justify-center rounded-lg border border-border px-4 text-sm font-medium text-ink hover:bg-surface2"
          >
            {t('auth.signIn')}
          </a>
        </div>
      </AuthShell>
    )
  }

  return (
    <AuthShell title={t('auth.twoFaTitle')} subtitle={t('auth.twoFaSub')}>
      <TwoFactorForm
        onSubmit={(code) => verify2FA(stored, code)}
        onVerified={() => navigate(next, { replace: true })}
        cancelHref="/login"
      />
    </AuthShell>
  )
}