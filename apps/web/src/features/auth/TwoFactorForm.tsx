import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Input } from '@/components/ui/Input'
import { Button } from '@/components/ui/Button'
import { Spinner } from '@/components/ui/Spinner'
import { ApiError } from '@/lib/api'
import { useForm } from './AuthShell'

/**
 * The second-factor form, shared by both ways a first factor can succeed.
 *
 * It is one component because there are two entry points and they must not
 * drift: `LoginPage` (password succeeded, TOTP enrolled) and `/2fa` (a Google
 * OAuth login succeeded, TOTP enrolled). The second path did not exist until
 * the OAuth callback was made to honour the 2FA gate - before that, an OAuth
 * login minted a full session and the enrolled factor was never asked for,
 * which made Google login a complete bypass of 2FA.
 *
 * The challenge is NOT a prop. Each caller already holds it and closes over it
 * in `onSubmit`; passing it as well would create two sources of truth for the
 * same token, and the one that wins would depend on which prop the component
 * happened to read.
 *
 * `onVerified` is what each caller does on success, and they differ: LoginPage
 * navigates to `?next=`, `/2fa` navigates to /me. Keeping that in the callers
 * is deliberate - it is the one behaviour that is genuinely route-specific.
 */
export function TwoFactorForm({
  onVerified,
  onSubmit,
  cancelHref,
}: {
  onVerified: () => void
  onSubmit: (code: string) => Promise<void>
  cancelHref?: string
}) {
  const { t } = useTranslation()
  const { values, set } = useForm({ code: '' })
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [general, setGeneral] = useState('')
  const [pending, setPending] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErrors({})
    setGeneral('')
    setPending(true)
    try {
      await onSubmit(values.code.trim())
      onVerified()
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
    <form onSubmit={submit} className="space-y-4" noValidate>
      {general && (
        // role="alert" because a failed second factor is announced, not merely
        // displayed: a screen-reader user who submitted a code and heard
        // nothing would assume it was still pending.
        <p
          role="alert"
          className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400"
        >
          {general}
        </p>
      )}
      <p className="text-sm text-ink3">{t('auth.twoFaHint')}</p>
      <Input
        label={t('auth.twoFaCode')}
        inputMode="text"
        // one-time-code is what lets an iOS or Android password manager offer
        // the code straight from the authenticator app.
        autoComplete="one-time-code"
        autoFocus
        required
        value={values.code}
        onChange={set('code')}
        error={errors.code}
      />
      <Button type="submit" fullWidth aria-busy={pending} className="mt-2">
        {pending ? <Spinner /> : t('auth.signIn')}
      </Button>
      {cancelHref && (
        <p className="text-center">
          <a href={cancelHref} className="text-xs text-ink3 hover:text-ink">
            {t('auth.twoFaCancel')}
          </a>
        </p>
      )}
    </form>
  )
}