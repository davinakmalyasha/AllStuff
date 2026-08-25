import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { MailCheck } from 'lucide-react'
import { AuthShell, useForm } from './AuthShell'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ApiError, post } from '@/lib/api'
import { useAuth } from '@/stores/auth'

export function RestorePage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const fetchMe = useAuth((s) => s.fetchMe)
  const { values, set } = useForm({ email: '', password: '' })
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [general, setGeneral] = useState('')
  const [windowClosed, setWindowClosed] = useState(false)
  const [pending, setPending] = useState(false)
  const [done, setDone] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErrors({})
    setGeneral('')
    setWindowClosed(false)
    setPending(true)
    try {
      await post('/auth/restore', { email: values.email.trim(), password: values.password })
      // Session cookies are live now: hydrate the user before the redirect so
      // guards see an authenticated visitor immediately.
      setDone(true)
      await fetchMe()
      setTimeout(() => navigate('/me', { replace: true }), 1200)
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.code === 'restore_window_expired' || err.status === 410 || err.code === 'account_restore_expired') {
          setWindowClosed(true)
        } else {
          setErrors(err.fields ?? {})
          if (!err.fields) setGeneral(err.message)
        }
      } else {
        setGeneral(t('common.error'))
      }
    } finally {
      setPending(false)
    }
  }

  return (
    <AuthShell title={t('auth.restoreTitle')} subtitle={t('auth.restoreSub')}>
      {done ? (
        <div className="flex flex-col items-center gap-3 rounded-lg border border-border bg-surface px-4 py-6 text-center">
          <MailCheck className="h-8 w-8 text-ink" aria-hidden />
          <p className="text-sm text-ink2">{t('auth.restoreDone')}</p>
          <Spinner />
        </div>
      ) : (
        <form onSubmit={submit} className="space-y-4" noValidate>
          {windowClosed && (
            <div
              role="alert"
              className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-400"
            >
              {t('auth.restoreWindowClosed')}
            </div>
          )}
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
          <Input
            label={t('auth.password')}
            type="password"
            autoComplete="current-password"
            required
            value={values.password}
            onChange={set('password')}
            error={errors.password}
          />
          <Button type="submit" fullWidth disabled={pending} className="mt-2">
            {pending ? <Spinner /> : t('auth.restoreCta')}
          </Button>
        </form>
      )}
    </AuthShell>
  )
}
