import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { AuthShell, AuthFooterLink, useForm } from './AuthShell'
import { GoogleButton } from '@/components/ui/GoogleButton'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ApiError } from '@/lib/api'
import { useAuth } from '@/stores/auth'

export function LoginPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { login, verify2FA } = useAuth()
  const twoFaChallenge = useAuth((s) => s.twoFaChallenge)
  const [params] = useSearchParams()
  // Open-redirect guard: only same-site relative paths survive. Protocol-
  // relative ("//evil.com") and absolute URLs fall back to /me.
  const rawNext = params.get('next') ?? '/me'
  const next = rawNext.startsWith('/') && !rawNext.startsWith('//') ? rawNext : '/me'
  const { values, set } = useForm({ email: '', password: '', code: '' })
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [general, setGeneral] = useState('')
  const [pending, setPending] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErrors({})
    setGeneral('')
    setPending(true)
    try {
      if (twoFaChallenge) {
        await verify2FA(twoFaChallenge, values.code.trim())
      } else {
        await login(values.email, values.password)
      }
      navigate(next, { replace: true })
    } catch (err) {
      if (err instanceof ApiError) {
        setErrors(err.fields ?? {})
        if (!err.fields) setGeneral(err.message)
      } else {
        setGeneral(t('common.error'))
      }
    } finally {
      setPending(false)
    }
  }

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
            {general && (
              <p className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
                {general}
              </p>
            )}
            <p className="text-sm text-ink3">
              Enter the 6-digit code from your authenticator app, or a recovery code.
            </p>
            <Input
              label="Authentication code"
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
            <GoogleButton />
            <div className="flex items-center gap-3">
              <span className="h-px flex-1 bg-border" />
              <span className="text-xs text-ink3">or</span>
              <span className="h-px flex-1 bg-border" />
            </div>
            <form onSubmit={submit} className="space-y-4" noValidate>
              {general && (
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
