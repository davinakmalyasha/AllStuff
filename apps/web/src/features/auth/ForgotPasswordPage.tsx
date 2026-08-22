import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { MailCheck } from 'lucide-react'
import { AuthShell, useForm } from './AuthShell'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { useAuth } from '@/stores/auth'

export function ForgotPasswordPage() {
  const { t } = useTranslation()
  const { forgotPassword } = useAuth()
  const { values, set } = useForm({ email: '' })
  const [pending, setPending] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState('')

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setPending(true)
    setError('')
    try {
      await forgotPassword(values.email)
      setSent(true)
    } catch {
      setError(t('common.error'))
    } finally {
      setPending(false)
    }
  }

  if (sent) {
    return (
      <AuthShell title={t('auth.forgotTitle')} subtitle="">
        <div className="flex flex-col items-center gap-3 rounded-lg border border-border bg-surface px-4 py-6 text-center">
          <MailCheck className="h-8 w-8 text-ink" aria-hidden />
          <p className="text-sm text-ink2">{t('auth.checkInbox')}</p>
        </div>
      </AuthShell>
    )
  }

  return (
    <AuthShell title={t('auth.forgotTitle')} subtitle={t('auth.forgotSub')}>
      <form onSubmit={submit} className="space-y-4" noValidate>
        {error && (
          <p className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
            {error}
          </p>
        )}
        <Input
          label={t('auth.email')}
          type="email"
          autoComplete="email"
          required
          value={values.email}
          onChange={set('email')}
        />
        <Button type="submit" fullWidth disabled={pending}>
          {pending ? <Spinner /> : t('auth.sendReset')}
        </Button>
      </form>
    </AuthShell>
  )
}
