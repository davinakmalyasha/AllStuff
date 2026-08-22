import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { CheckCircle2 } from 'lucide-react'
import { AuthShell } from './AuthShell'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ApiError } from '@/lib/api'
import { useAuth } from '@/stores/auth'

export function VerifyEmailPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { verifyEmail, fetchMe } = useAuth()
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''

  const [state, setState] = useState<'idle' | 'pending' | 'done' | 'error'>('idle')
  const [message, setMessage] = useState('')

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
      </div>
    </AuthShell>
  )
}
