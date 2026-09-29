import { useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { safeInternalPath } from '@/lib/url'
import { AuthShell, AuthFooterLink, useForm } from './AuthShell'
import { GoogleButton } from '@/components/ui/GoogleButton'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ApiError } from '@/lib/api'
import { useAuth, useAuthState } from '@/stores/auth'

export function LoginPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { login, verify2FA } = useAuthState((s) => ({ login: s.login, verify2FA: s.verify2FA }))
  const twoFaChallenge = useAuth((s) => s.twoFaChallenge)
  const [params] = useSearchParams()
  // Return path. Two independent reasons this is stricter than a regex:
  //
  //  * `//evil.com` and `/\evil.com` are both cross-origin, and WHATWG treats a
  //    backslash as a slash in special schemes, so both must be rejected.
  //  * The WHATWG URL parser STRIPS U+0009/U+000A/U+000D from URLs, so
  //    "/\t/evil.com" survives a naive character check and then normalises to
  //    "//evil.com" inside history.pushState. Control characters are therefore
  //    rejected outright rather than "not matched by the first class".
  //
  // The final authority is the URL parser: parse against the current origin and
  // only accept a same-origin result. That is immune to the classes above rather
  // than enumerating them.
  const next = useMemo(() => safeInternalPath(params.get('next'), '/me'), [params])
  const { values, set } = useForm({ email: '', password: '', code: '' })
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [general, setGeneral] = useState('')
  const [pending, setPending] = useState(false)
  // Login refused because the account sits in its deletion grace period:
  // surface the restore path instead of a dead-end error.
  const [pendingDeletion, setPendingDeletion] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErrors({})
    setGeneral('')
    setPendingDeletion(false)
    setPending(true)
    try {
      if (twoFaChallenge) {
        await verify2FA(twoFaChallenge, values.code.trim())
      } else {
        await login(values.email, values.password)
      }
      // Do NOT navigate yet. `login` resolves normally when the account has 2FA
      // enrolled — it stores a challenge and returns — so navigating here fired
      // a redirect while still unauthenticated: RequireAuth bounced to
      // /login?next=/me, OVERWRITING the original ?next. A 2FA user deep-linking
      // to /dashboard/billing was therefore sent to /me and lost their
      // destination.
      //
      // Gate on the store instead: the user is present only once a real session
      // cookie has been issued.
      if (useAuth.getState().user) navigate(next, { replace: true })
    } catch (err) {
      if (err instanceof ApiError && err.code === 'account_pending_deletion') {
        setPendingDeletion(true)
      } else if (err instanceof ApiError) {
        setErrors(err.fields ?? {})
        if (!err.fields) setGeneral(err.message)
      } else {
        setGeneral(t('common.error'))
      }
    } finally {
      setPending(false)
    }
  }

  const deletionNotice = (
    <div
      role="alert"
      className="rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-300"
    >
      <p className="font-medium">{t('auth.pendingDeletionTitle')}</p>
      <p className="mt-0.5">
        {t('auth.pendingDeletionBody')}{' '}
        <Link to="/auth/restore" className="font-semibold underline underline-offset-2">
          {t('auth.restoreLink')}
        </Link>
      </p>
    </div>
  )

  return (
    <AuthShell
      title={t('auth.loginTitle')}
      subtitle={t('auth.loginSub')}
      footer={
        <AuthFooterLink text={t('auth.noAccount')} to="/register" label={t('auth.createAccount')} />
      }
    >
      <div className="space-y-4">
        {twoFaChallenge ? (
          <form onSubmit={submit} className="space-y-4" noValidate>
            {pendingDeletion ? deletionNotice : general && (
              <p className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
                {general}
              </p>
            )}
            <p className="text-sm text-ink3">
              {t('auth.twoFaHint')}
            </p>
            <Input
              label={t('auth.twoFaCode')}
              inputMode="text"
              autoComplete="one-time-code"
              autoFocus
              required
              value={values.code}
              onChange={set('code')}
              error={errors.code}
            />
            <Button type="submit" fullWidth disabled={pending} className="mt-2">
              {pending ? <Spinner /> : t('auth.signIn')}
            </Button>
          </form>
        ) : (
          <>
            <GoogleButton label={t('auth.googleContinue')} />
            <div className="flex items-center gap-3">
              <span className="h-px flex-1 bg-border" />
              <span className="text-xs text-ink3">{t('auth.orDivider')}</span>
              <span className="h-px flex-1 bg-border" />
            </div>
            <form onSubmit={submit} className="space-y-4" noValidate>
              {pendingDeletion ? deletionNotice : general && (
                <p className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
                  {general}
                </p>
              )}
              <Input
                label={t('auth.email')}
                type="email"
                autoComplete="email"
                required
                value={values.email}
                onChange={set('email')}
                error={errors.email}
              />
              <div>
                <Input
                  label={t('auth.password')}
                  type="password"
                  autoComplete="current-password"
                  required
                  value={values.password}
                  onChange={set('password')}
                  error={errors.password}
                />
                <div className="mt-2 text-right">
                  <Link to="/forgot-password" className="text-xs text-ink3 hover:text-ink">
                    {t('auth.forgotPassword')}
                  </Link>
                </div>
              </div>
              <Button type="submit" fullWidth disabled={pending} className="mt-2">
                {pending ? <Spinner /> : t('auth.signIn')}
              </Button>
            </form>
          </>
        )}
      </div>
    </AuthShell>
  )
}
