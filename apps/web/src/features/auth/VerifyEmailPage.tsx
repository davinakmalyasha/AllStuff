import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { CheckCircle2 } from 'lucide-react'
import { AuthShell } from './AuthShell'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ApiError, post } from '@/lib/api'
import { useAuth } from '@/stores/auth'

const RESEND_COOLDOWN_S = 60

export function VerifyEmailPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const user = useAuth((s) => s.user)
  const { verifyEmail, fetchMe } = useAuth()
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''

  const [state, setState] = useState<'idle' | 'pending' | 'done' | 'error'>('idle')
  const [message, setMessage] = useState('')

  // Resend (POST /auth/resend-verification): prefilled for signed-in users,
  // with a local 60s countdown so the endpoint can't be hammered.
  const [resendEmail, setResendEmail] = useState(user?.email ?? '')
  const [resendPending, setResendPending] = useState(false)
  const [resent, setResent] = useState(false)
  const [resendError, setResendError] = useState('')
  const [cooldown, setCooldown] = useState(0)

  // fetchMe may resolve after mount: fill the field without clobbering typing.
  useEffect(() => {
    setResendEmail((prev) => prev || user?.email || '')
  }, [user?.email])

  useEffect(() => {
    if (cooldown <= 0) return
    const timer = setInterval(() => setCooldown((s) => (s > 0 ? s - 1 : 0)), 1000)
    return () => clearInterval(timer)
  }, [cooldown])

  const submit = async () => {
    setState('pending')
    try {
      await verifyEmail(token)
      await fetchMe()
      setState('done')
    } catch (err) {
      setState('error')
      setMessage(err instanceof ApiError ? err.message : t('common.error'))
    }
  }

  const resend = async () => {
    setResendPending(true)
    setResendError('')
    try {
      await post('/auth/resend-verification', { email: resendEmail.trim() })
      setResent(true)
      setCooldown(RESEND_COOLDOWN_S)
    } catch (err) {
      setResendError(err instanceof ApiError ? err.message : t('common.error'))
    } finally {
      setResendPending(false)
    }
  }

  return (
    <AuthShell title={t('auth.verifyTitle')} subtitle={t('auth.verifySub')}>
      <div className="space-y-4">
        {state === 'done' && (
          <div className="flex flex-col items-center gap-3 rounded-lg border border-border bg-surface px-4 py-6 text-center">
            <CheckCircle2 className="h-8 w-8 text-ink" aria-hidden />
            <p className="text-sm text-ink2">{t('auth.verifyDone')}</p>
            <Button onClick={() => navigate('/me')}>{t('nav.profile')}</Button>
          </div>
        )}
        {state === 'error' && (
          <p className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
            {message}
          </p>
        )}
        {state !== 'done' && (
          <Button fullWidth onClick={submit} disabled={state === 'pending' || !token}>
            {state === 'pending' ? <Spinner /> : t('auth.verifyCta')}
          </Button>
        )}

        <div className="space-y-2 rounded-lg border border-border bg-surface px-4 py-4">
          <p className="text-sm font-medium text-ink">{t('auth.verifySub')}</p>
          <p className="text-xs text-ink3">Didn't get the email? Send it again.</p>
          <div className="flex items-end gap-2">
            <Input
              label={t('auth.email')}
              type="email"
              autoComplete="email"
              value={resendEmail}
              onChange={(e) => setResendEmail(e.target.value)}
            />
            <Button
              variant="secondary"
              onClick={() => void resend()}
              disabled={resendPending || cooldown > 0 || !resendEmail.trim() || state === 'done'}
              className="shrink-0"
            >
              {resendPending ? (
                <Spinner />
              ) : cooldown > 0 ? (
                t('auth.resendIn', { seconds: cooldown })
              ) : (
                t('auth.resendVerification')
              )}
            </Button>
          </div>
          {resent && <p className="text-xs text-ink2">{t('auth.resendSent')}</p>}
          {resendError && (
            <p className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-400">
              {resendError}
            </p>
          )}
        </div>
      </div>
    </AuthShell>
  )
}
