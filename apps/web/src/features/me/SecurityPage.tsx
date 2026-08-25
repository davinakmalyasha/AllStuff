import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bell, Download, KeyRound, ShieldCheck, Smartphone, Trash2 } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Modal } from '@/components/ui/Modal'
import { Input } from '@/components/ui/Input'
import { usePageMeta } from '@/lib/meta'
import { toast } from '@/components/ui/Toast'
import { CURRENCIES, useCurrency } from '@/stores/currency'

interface SessionDTO {
  id: string
  ip: string | null
  user_agent: string | null
  created_at: string
  last_seen_at: string
  revoked_at: string | null
}

interface HistoryDTO {
  event: string
  ip: string | null
  user_agent: string | null
  created_at: string
}

const EMAIL_ALERT_TYPES: [string, string][] = [
  ['message_received', 'New messages'],
  ['verification_result', 'Verification decisions'],
  ['doc_re_request', 'Document requests'],
  ['business_suspended', 'Business suspensions'],
  ['business_restored', 'Business restorations'],
  ['moderation_warning', 'Moderation notices'],
  ['appeal_result', 'Appeal decisions'],
]

/** Security center (PRD §5.9.2): 2FA, sessions, login history, export,
 *  push notifications, notification preferences, account deletion. */
export function SecurityPage() {
  const qc = useQueryClient()
  usePageMeta('Security')
  const { display, setDisplay } = useCurrency()
  const [step, setStep] = useState<'idle' | 'enrolled'>('idle')
  const [secret, setSecret] = useState('')
  const [otpauth, setOtpauth] = useState('')
  const [code, setCode] = useState('')
  const [recovery, setRecovery] = useState<string[] | null>(null)
  const [disableCode, setDisableCode] = useState('')
  const [pushState, setPushState] = useState<'idle' | 'subscribing' | 'subscribed' | 'unsupported'>('idle')
  const [delPassword, setDelPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [currentPassword, setCurrentPassword] = useState('')
  const [newEmail, setNewEmail] = useState('')
  const [emailPassword, setEmailPassword] = useState('')
  const [regenCode, setRegenCode] = useState('')
  const [regenCodes, setRegenCodes] = useState<string[] | null>(null)

  const changePassword = useMutation({
    mutationFn: () => api('/me/password', { method: 'POST', body: { current_password: currentPassword, new_password: newPassword } }),
    onSuccess: () => {
      setCurrentPassword('')
      setNewPassword('')
      toast.success('Password updated')
    },
  })

  const changeEmail = useMutation({
    mutationFn: () => api('/me/email', { method: 'POST', body: { password: emailPassword, email: newEmail } }),
    onSuccess: () => {
      setEmailPassword('')
      setNewEmail('')
      toast.success('Email updated — verify the new address to keep full access')
    },
  })

  const regen = useMutation({
    mutationFn: () => api<{ recovery_codes: string[] }>('/me/security/2fa/recovery-codes', { method: 'POST', body: { code: regenCode } }),
    onSuccess: (r) => {
      setRegenCodes(r.recovery_codes)
      setRegenCode('')
    },
  })

  const { data: status } = useQuery({
    queryKey: ['2fa-status'],
    queryFn: () => api<{ enabled: boolean }>('/me/security'),
  })
  const { data: sessions } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => api<{ sessions: SessionDTO[]; history: HistoryDTO[] }>('/me/security/sessions'),
  })

  const enroll = useMutation({
    mutationFn: () => api<{ secret: string; otpauth_url: string }>('/me/security/2fa', { method: 'POST' }),
    onSuccess: (r) => {
      setSecret(r.secret)
      setOtpauth(r.otpauth_url)
      setStep('enrolled')
    },
  })
  const confirm = useMutation({
    mutationFn: () => api<{ enabled: boolean; recovery_codes: string[] }>('/me/security/2fa/confirm', { method: 'POST', body: { code } }),
    onSuccess: (r) => {
      setRecovery(r.recovery_codes)
      qc.invalidateQueries({ queryKey: ['2fa-status'] })
    },
  })
  const disable = useMutation({
    mutationFn: () => api('/me/security/2fa', { method: 'DELETE', body: { code: disableCode } }),
    onSuccess: () => {
      setDisableCode('')
      qc.invalidateQueries({ queryKey: ['2fa-status'] })
    },
  })
  const revoke = useMutation({
    mutationFn: (id: string) => api(`/me/security/sessions/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['sessions'] }),
  })
  const revokeOthers = useMutation({
    mutationFn: () => api('/me/security/sessions/revoke-others', { method: 'POST' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['sessions'] }),
  })

  const { data: prefs } = useQuery({
    queryKey: ['notif-prefs'],
    queryFn: () =>
      api<{ channels?: Record<string, unknown>; quiet_hours?: Record<string, unknown>; digest_opt_in?: boolean }>(
        '/me/notifications-settings',
      ),
  })
  const savePrefs = useMutation({
    mutationFn: (body: Record<string, unknown>) => api('/me/notifications-settings', { method: 'PATCH', body }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['notif-prefs'] }),
  })

  const deleteAccount = useMutation({
    mutationFn: () => api('/me/delete', { method: 'POST', body: { password: delPassword } }),
    onSuccess: () => {
      window.location.href = '/'
    },
  })

  // Public API keys (PRD §9.6).
  const [keyOpen, setKeyOpen] = useState(false)
  const [keyName, setKeyName] = useState('')
  const [freshKey, setFreshKey] = useState<string | null>(null)
  const { data: keyData } = useQuery({
    queryKey: ['api-keys'],
    queryFn: () => api<{ keys: Array<{ id: string; name: string; prefix: string; revoked_at: string | null }> }>('/me/api-keys'),
  })
  const apiKeys = keyData?.keys ?? []
  const createKey = useMutation({
    mutationFn: () =>
      api<{ raw_key: string }>('/me/api-keys', { method: 'POST', body: { name: keyName } }),
    onSuccess: (r) => {
      setFreshKey(r.raw_key)
      setKeyOpen(false)
      setKeyName('')
      qc.invalidateQueries({ queryKey: ['api-keys'] })
    },
  })
  const revokeKey = useMutation({
    mutationFn: (id: string) => api(`/me/api-keys/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['api-keys'] }),
  })

  // Web Push enrollment (PRD §5.5.3).
  const enablePush = async () => {
    if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
      setPushState('unsupported')
      return
    }
    setPushState('subscribing')
    try {
      const reg = await navigator.serviceWorker.register('/sw.js')
      const keyResp = await api<{ public_key: string }>('/push/vapid-key')
      const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(keyResp.public_key) as unknown as BufferSource,
      })
      await api('/push/subscribe', {
        method: 'POST',
        body: { endpoint: sub.endpoint, keys: { p256dh: btoa(String.fromCharCode(...new Uint8Array(sub.getKey('p256dh')!))), auth: btoa(String.fromCharCode(...new Uint8Array(sub.getKey('auth')!))) } },
      })
      setPushState('subscribed')
    } catch {
      setPushState('unsupported')
    }
  }

  useEffect(() => {
    if ('serviceWorker' in navigator && 'PushManager' in window) {
      void navigator.serviceWorker.getRegistration().then((reg) => {
        if (reg) void reg.pushManager.getSubscription().then((s) => setPushState(s ? 'subscribed' : 'idle'))
      })
    }
  }, [])

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <div>
        <p className="mono-label mb-1">Security</p>
        <h1 className="text-2xl font-semibold tracking-tight">Account security</h1>
      </div>

      {/* 2FA */}
      <Card className="space-y-4">
        <div className="flex items-center gap-3">
          <ShieldCheck className="h-5 w-5 text-ink" />
          <div className="flex-1">
            <p className="text-sm font-semibold text-ink">Two-factor authentication</p>
            <p className="text-xs text-ink3">TOTP authenticator app. Required for admin accounts.</p>
          </div>
          {status?.enabled ? (
            <span className="rounded-full bg-surface2 px-2.5 py-1 text-xs text-ink2">Enabled</span>
          ) : (
            <Button size="sm" onClick={() => void enroll.mutateAsync()} disabled={enroll.isPending}>
              <Smartphone className="h-4 w-4" /> Enable
            </Button>
          )}
        </div>

        {step === 'enrolled' && !status?.enabled && (
          <div className="space-y-3 border-t border-border pt-4">
            <p className="text-sm text-ink2">Scan this secret with your authenticator app (e.g. Google Authenticator, Authy):</p>
            <Input label="Secret key" value={secret} readOnly />
            <Input label="otpauth URL" value={otpauth} readOnly />
            <div className="flex gap-2">
              <Input label="6-digit code" value={code} onChange={(e) => setCode(e.target.value)} placeholder="000000" />
              <Button className="mt-6" onClick={() => void confirm.mutateAsync()} disabled={code.length !== 6 || confirm.isPending}>
                <KeyRound className="h-4 w-4" /> Confirm
              </Button>
            </div>
            {confirm.error && <p className="text-sm text-red-600 dark:text-red-400">{(confirm.error as Error).message}</p>}
          </div>
        )}

        {recovery && (
          <div className="space-y-2 border-t border-border pt-4">
            <p className="text-sm font-medium text-ink">Recovery codes — store these somewhere safe. Each works once.</p>
            <div className="grid grid-cols-2 gap-2 font-mono text-sm">
              {recovery.map((c) => <code key={c} className="rounded bg-surface2 px-2 py-1">{c}</code>)}
            </div>
          </div>
        )}

        {status?.enabled && (
          <div className="flex items-end gap-2 border-t border-border pt-4">
            <Input label="Current code to disable" value={disableCode} onChange={(e) => setDisableCode(e.target.value)} placeholder="000000" />
            <Button variant="danger" onClick={() => void disable.mutateAsync()} disabled={disableCode.length !== 6 || disable.isPending}>Disable</Button>
          </div>
        )}

        {status?.enabled && (
          <div className="border-t border-border pt-4">
            <p className="mono-label mb-2">Recovery codes</p>
            <div className="flex items-end gap-2">
              <Input label="Current code" value={regenCode} onChange={(e) => setRegenCode(e.target.value)} placeholder="000000" />
              <Button variant="secondary" onClick={() => void regen.mutateAsync()} disabled={regenCode.length !== 6 || regen.isPending}>Regenerate</Button>
            </div>
            {regen.error && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{(regen.error as Error).message}</p>}
            {regenCodes && (
              <div className="mt-3 grid grid-cols-2 gap-2 font-mono text-sm">
                {regenCodes.map((c) => <code key={c} className="rounded bg-surface2 px-2 py-1">{c}</code>)}
              </div>
            )}
          </div>
        )}
      </Card>

      {/* Password & email */}
      <Card className="space-y-4">
        <p className="mono-label">Sign-in details</p>
        <div className="grid gap-4 sm:grid-cols-2">
          <Input label="Current password" type="password" autoComplete="current-password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} placeholder="••••••••" />
          <Input label="New password" type="password" autoComplete="new-password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} placeholder="At least 8 characters" />
        </div>
        <Button size="sm" onClick={() => void changePassword.mutateAsync()} disabled={!currentPassword || newPassword.length < 8 || changePassword.isPending}>Change password</Button>
        {changePassword.error && <p className="text-sm text-red-600 dark:text-red-400">{(changePassword.error as Error).message}</p>}
        <div className="grid gap-4 border-t border-border pt-4 sm:grid-cols-2">
          <Input label="New email" type="email" value={newEmail} onChange={(e) => setNewEmail(e.target.value)} placeholder="new@example.com" />
          <Input label="Password" type="password" autoComplete="current-password" value={emailPassword} onChange={(e) => setEmailPassword(e.target.value)} placeholder="Confirm with password" />
        </div>
        <Button size="sm" variant="secondary" onClick={() => void changeEmail.mutateAsync()} disabled={!newEmail.includes('@') || !emailPassword || changeEmail.isPending}>Change email</Button>
        {changeEmail.error && <p className="text-sm text-red-600 dark:text-red-400">{(changeEmail.error as Error).message}</p>}
      </Card>

      {/* Sessions */}
      <Card className="space-y-3">
        <div className="flex items-center justify-between">
          <p className="mono-label">Active sessions</p>
          <button onClick={() => void revokeOthers.mutateAsync()} className="text-xs text-ink3 hover:text-ink">
            Revoke all others
          </button>
        </div>
        {sessions?.sessions.map((s) => (
          <div key={s.id} className="flex items-center gap-3 text-sm">
            <span className={`h-2 w-2 rounded-full ${s.revoked_at ? 'bg-ink3' : 'bg-ink'}`} />
            <span className="min-w-0 flex-1 truncate text-ink2">{s.user_agent ?? 'Unknown device'} · {s.ip ?? '—'}</span>
            <span className="text-xs text-ink3">{new Date(s.last_seen_at).toLocaleString()}</span>
            {!s.revoked_at && (
              <button onClick={() => void revoke.mutateAsync(s.id)} className="text-xs text-ink3 hover:text-ink">Revoke</button>
            )}
          </div>
        ))}
        <p className="mono-label pt-2">Login history</p>
          {(sessions?.history ?? []).map((h, i) => (
          <div key={i} className="flex items-center gap-3 text-sm">
            <span className="font-mono text-xs text-ink3">{h.event}</span>
            <span className="flex-1 truncate text-xs text-ink3">{h.ip ?? '—'}</span>
            <span className="text-xs text-ink3">{new Date(h.created_at).toLocaleString()}</span>
          </div>
        ))}
      </Card>

      {/* Export + currency */}
      <Card className="flex items-center justify-between">
        <div>
          <p className="text-sm font-semibold text-ink">Export your data</p>
          <p className="text-xs text-ink3">Full JSON snapshot: profile, businesses, products, reviews, comments, collections (PRD §5.9.2).</p>
        </div>
        <a href="/api/v1/me/export" download>
          <Button variant="secondary"><Download className="h-4 w-4" /> Export</Button>
        </a>
      </Card>

      <Card className="flex items-center justify-between">
        <div>
          <p className="text-sm font-semibold text-ink">Display currency</p>
          <p className="text-xs text-ink3">Prices convert automatically across the platform (PRD D5).</p>
        </div>
        <select value={display} onChange={(e) => setDisplay(e.target.value)} className="h-10 rounded-lg border border-border bg-surface px-3 text-sm text-ink">
          {CURRENCIES.map((c) => <option key={c} value={c}>{c}</option>)}
        </select>
      </Card>

      {/* Push + preferences */}
      <Card className="space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm font-semibold text-ink">Push notifications</p>
            <p className="text-xs text-ink3">Browser notifications when you're not on BizVerse (PRD §5.5.3).</p>
          </div>
          <Button variant="secondary" size="sm" onClick={() => void enablePush()} disabled={pushState === 'subscribing' || pushState === 'subscribed'}>
            <Bell className="h-4 w-4" /> {pushState === 'subscribed' ? 'Enabled' : pushState === 'unsupported' ? 'Unsupported' : 'Enable'}
          </Button>
        </div>
        <div className="border-t border-border pt-3">
          <p className="mono-label mb-2">Weekly digest</p>
          <p className="mb-2 text-xs text-ink3">A Monday email with what's trending in your saved categories and collections.</p>
          <label className="flex items-center gap-2 text-sm text-ink2">
            <input
              type="checkbox"
              checked={Boolean(prefs?.digest_opt_in)}
              onChange={(e) => void savePrefs.mutateAsync({ ...prefs, digest_opt_in: e.target.checked })}
              className="h-3.5 w-3.5 accent-black dark:accent-white"
            />
            Receive the weekly digest email
          </label>
        </div>
        <div className="border-t border-border pt-3">
          <p className="mono-label mb-2">Email alerts</p>
          <p className="mb-2 text-xs text-ink3">Get email copies for important events (PRD §5.7 channel matrix).</p>
          <div className="grid gap-1.5">
            {EMAIL_ALERT_TYPES.map(([type, label]) => {
              const enabled = (prefs?.channels?.email as string[] | undefined)?.includes(type) ?? false
              return (
                <label key={type} className="flex items-center gap-2 text-sm text-ink2">
                  <input
                    type="checkbox"
                    checked={enabled}
                    onChange={(e) => {
                      const current = Array.isArray(prefs?.channels?.email) ? (prefs!.channels!.email as string[]) : []
                      const next = e.target.checked ? [...current, type] : current.filter((t) => t !== type)
                      void savePrefs.mutateAsync({ ...prefs, channels: { ...(prefs?.channels ?? {}), email: next } })
                    }}
                    className="h-3.5 w-3.5 accent-black dark:accent-white"
                  />
                  {label}
                </label>
              )
            })}
          </div>
        </div>
        <div className="border-t border-border pt-3">
          <p className="mono-label mb-2">Quiet hours</p>
          <label className="flex items-center gap-2 text-sm text-ink2">
            <input
              type="checkbox"
              checked={Boolean((prefs?.quiet_hours as Record<string, unknown> | undefined)?.enabled)}
              onChange={(e) =>
                void savePrefs.mutateAsync({ ...prefs, quiet_hours: { ...((prefs?.quiet_hours as Record<string, unknown>) ?? {}), enabled: e.target.checked } })
              }
              className="h-3.5 w-3.5 accent-black dark:accent-white"
            />
            Mute push & email between 22:00 and 08:00
          </label>
        </div>
      </Card>

      {/* API keys (PRD §9.6) */}
      <Card className="space-y-4">
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm font-semibold text-ink">API keys</p>
            <p className="text-xs text-ink3">Read-only access to the public API via <code className="font-mono">X-API-Key</code> (300 req/min per key).</p>
          </div>
          <Button variant="secondary" size="sm" onClick={() => setKeyOpen(true)}>New key</Button>
        </div>
        {apiKeys.length === 0 ? (
          <p className="text-sm text-ink3">No keys yet. Create one to build on the public API.</p>
        ) : (
          <ul className="space-y-2">
            {apiKeys.map((k) => (
              <li key={k.id} className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-ink">{k.name}</p>
                  <p className="font-mono text-xs text-ink3">{k.prefix}…</p>
                </div>
                {k.revoked_at ? (
                  <span className="text-xs text-ink3">revoked</span>
                ) : (
                  <button onClick={() => void revokeKey.mutateAsync(k.id)} className="text-xs text-ink3 hover:text-ink">Revoke</button>
                )}
              </li>
            ))}
          </ul>
        )}
        {freshKey && (
          <div className="rounded-lg border border-emerald-300 bg-emerald-50 p-3 dark:border-emerald-900 dark:bg-emerald-950/30">
            <p className="text-sm font-medium text-emerald-800 dark:text-emerald-300">Key created — copy it now, it won't be shown again:</p>
            <code className="mt-1 block break-all font-mono text-xs">{freshKey}</code>
            <button onClick={() => void navigator.clipboard.writeText(freshKey)} className="mt-2 text-xs underline underline-offset-2">Copy</button>
          </div>
        )}
        <Modal open={keyOpen} onClose={() => setKeyOpen(false)} title="New API key">
          <div className="space-y-3">
            <input
              value={keyName}
              onChange={(e) => setKeyName(e.target.value)}
              placeholder="e.g. My analytics app"
              className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink"
              autoFocus
            />
            <div className="flex justify-end">
              <Button size="sm" onClick={() => void createKey.mutateAsync()} disabled={!keyName.trim() || createKey.isPending}>
                Create
              </Button>
            </div>
          </div>
        </Modal>
      </Card>

      {/* Account deletion */}
      <Card className="space-y-3 border-red-300 dark:border-red-900">
        <p className="mono-label text-red-700 dark:text-red-400">Danger zone</p>
        <p className="text-sm text-ink2">Deleting your account starts a 14-day grace period. You can cancel it any time before it completes.</p>
        <div className="flex items-end gap-2">
          <Input type="password" label="Confirm password" autoComplete="current-password" value={delPassword} onChange={(e) => setDelPassword(e.target.value)} placeholder="••••••••" />
          <Button variant="danger" onClick={() => void deleteAccount.mutateAsync()} disabled={!delPassword || deleteAccount.isPending}>
            <Trash2 className="h-4 w-4" /> Delete account
          </Button>
        </div>
        {deleteAccount.error && <p className="text-sm text-red-600 dark:text-red-400">{(deleteAccount.error as Error).message}</p>}
      </Card>
    </div>
  )
}

function urlBase64ToUint8Array(base64: string): Uint8Array {
  const padding = '='.repeat((4 - (base64.length % 4)) % 4)
  const raw = atob((base64 + padding).replace(/-/g, '+').replace(/_/g, '/'))
  const out = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}
