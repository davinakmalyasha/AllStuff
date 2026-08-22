import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { AuthShell, useForm } from './AuthShell'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ApiError } from '@/lib/api'
import { useAuth } from '@/stores/auth'

export function ResetPasswordPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { resetPassword } = useAuth()
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''

  const { values, set } = useForm({ password: '', confirm: '' })
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [pending, setPending] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErrors({})
    if (values.password !== values.confirm) {
      setErrors({ confirm: 'Passwords do not match.' })
      return
    }
    setPending(true)
    try {
      await resetPassword(token, values.password)
      navigate('/login', { replace: true })
    } catch (err) {
      if (err instanceof ApiError) {
        setErrors(err.fields ?? {})
        if (!err.fields) setErrors({ _: err.message })
      }
    } finally {
      setPending(false)
    }
  }

  return (
    <AuthShell title={t('auth.resetTitle')} subtitle={t('auth.resetSub')}>
      <form onSubmit={submit} className="space-y-4" noValidate>
        {errors._ && (
          <p className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
            {errors._}
          </p>
        )}
        <Input
          label={t('auth.password')}
          type="password"
          autoComplete="new-password"
          required
          value={values.password}
          onChange={set('password')}
          error={errors.password}
        />
        <Input
          label={t('auth.confirmPassword')}
          type="password"
          autoComplete="new-password"
          required
          value={values.confirm}
          onChange={set('confirm')}
          error={errors.confirm}
        />
        <Button type="submit" fullWidth disabled={pending || !token} className="mt-2">
          {pending ? <Spinner /> : t('auth.resetCta')}
        </Button>
      </form>
    </AuthShell>
  )
}
