import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { AuthShell, AuthFooterLink, useForm } from './AuthShell'
import { GoogleButton } from '@/components/ui/GoogleButton'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ApiError } from '@/lib/api'
import { useAuth } from '@/stores/auth'

export function RegisterPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { register } = useAuth()
  const [params] = useSearchParams()
  // Single leading slash not followed by / or \: rejects protocol-relative
  // "//evil.com" AND "/\evil.com" (WHATWG treats \ as / in special schemes).
  const rawNext = params.get('next') ?? '/me'
  const next = /^\/[^/\\]/.test(rawNext) ? rawNext : '/me'
  const { values, set } = useForm({
    email: '',
    password: '',
    confirm: '',
    name: '',
    username: '',
  })
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [pending, setPending] = useState(false)
  const [general, setGeneral] = useState('')

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErrors({})
    setGeneral('')
    if (values.password !== values.confirm) {
      setErrors({ confirm: 'Passwords do not match.' })
      return
    }
    setPending(true)
    try {
      await register({
        email: values.email,
        password: values.password,
        name: values.name,
        username: values.username,
      })
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
      title={t('auth.registerTitle')}
      subtitle={t('auth.registerSub')}
      footer={
        <AuthFooterLink text={t('auth.haveAccount')} to="/login" label={t('auth.signIn')} />
      }
    >
      <div className="space-y-4"><GoogleButton label="Sign up with Google" />
      <div className="flex items-center gap-3"><span className="h-px flex-1 bg-border" /><span className="text-xs text-ink3">or</span><span className="h-px flex-1 bg-border" /></div>
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
        <Input
          label={t('auth.name')}
          autoComplete="name"
          required
          value={values.name}
          onChange={set('name')}
          error={errors.name}
        />
        <Input
          label={t('auth.username')}
          autoComplete="username"
          required
          value={values.username}
          onChange={set('username')}
          hint={t('auth.usernameHint')}
          error={errors.username}
        />
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
        <Button type="submit" fullWidth disabled={pending} className="mt-2">
          {pending ? <Spinner /> : t('auth.createAccount')}
        </Button>
      </form></div>
    </AuthShell>
  )
}
