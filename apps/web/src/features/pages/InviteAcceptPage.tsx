import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Building2 } from 'lucide-react'
import { api } from '@/lib/api'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { PageSpinner } from '@/components/ui/Spinner'
import { usePageMeta } from '@/lib/meta'
import { useAuth } from '@/stores/auth'
import { toast } from '@/components/ui/Toast'

interface InviteInfoDTO {
  business_name: string
  inviter_name: string
  role: string
  email: string
}

/** Invite landing (/invite/:token): works signed-out (login first) and in. */
export function InviteAcceptPage() {
  const { token = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { user } = useAuth()
  const [pending, setPending] = useState(false)
  const [acceptError, setAcceptError] = useState('')

  usePageMeta('Team invite')

  const { data, isLoading, isError } = useQuery({
    queryKey: ['invite', token],
    queryFn: () => api<InviteInfoDTO>(`/invites/${encodeURIComponent(token)}`),
    enabled: !!token,
    retry: false,
  })

  const emailMismatch = !!user && !!data && user.email.toLowerCase() !== data.email.toLowerCase()

  const accept = async () => {
    setPending(true)
    setAcceptError('')
    try {
      await api(`/invites/${encodeURIComponent(token)}/accept`, { method: 'POST' })
      toast.success(`You're now a team member of ${data?.business_name ?? 'the business'}.`)
      // The dashboard reads the membership list — refresh it before landing.
      void qc.invalidateQueries({ queryKey: ['my-businesses'] })
      navigate('/dashboard')
    } catch (err) {
      setAcceptError(err instanceof Error ? err.message : 'Could not accept the invite.')
      setPending(false)
    }
  }

  if (isLoading) return <PageSpinner />
  if (isError || !data) {
    return (
      <div className="container-page flex min-h-[50vh] max-w-md flex-col items-center justify-center gap-3 text-center">
        <p className="font-mono text-5xl font-semibold tracking-tight">404</p>
        <p className="text-sm text-ink2">This invite is invalid, revoked, or expired.</p>
        <Link to="/" className="text-sm font-medium text-ink underline underline-offset-4">
          Back to home
        </Link>
      </div>
    )
  }

  return (
    <div className="container-page flex min-h-[60vh] max-w-md flex-col justify-center py-10">
      <Card className="space-y-5 p-6">
        <div className="flex items-center gap-3">
          <span className="flex h-12 w-12 items-center justify-center rounded-xl bg-surface2">
            <Building2 className="h-6 w-6 text-ink2" aria-hidden />
          </span>
          <div>
            <h1 className="text-lg font-semibold tracking-tight text-ink">Join {data.business_name}</h1>
            <p className="text-sm text-ink2">{data.inviter_name} invited you as a collaborator.</p>
          </div>
        </div>

        <dl className="space-y-1.5 rounded-lg bg-surface2 px-4 py-3 text-sm">
          <div className="flex items-center gap-2">
            <dt className="w-16 shrink-0 text-ink3">Business</dt>
            <dd className="truncate font-medium text-ink">{data.business_name}</dd>
          </div>
          <div className="flex items-center gap-2">
            <dt className="w-16 shrink-0 text-ink3">Invited by</dt>
            <dd className="truncate text-ink">{data.inviter_name}</dd>
          </div>
          <div className="flex items-center gap-2">
            <dt className="w-16 shrink-0 text-ink3">Role</dt>
            <dd>
              <Badge>{data.role}</Badge>
            </dd>
          </div>
          <div className="flex items-center gap-2">
            <dt className="w-16 shrink-0 text-ink3">Email</dt>
            <dd className="truncate text-ink">{data.email}</dd>
          </div>
        </dl>

        {acceptError && (
          <p className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
            {acceptError}
          </p>
        )}

        {!user ? (
          <>
            <p className="text-xs text-ink3">Sign in or create an account with {data.email} to accept this invite.</p>
            <Link to={`/login?next=${encodeURIComponent(`/invite/${token}`)}`} className="block">
              <Button fullWidth>Sign in to accept</Button>
            </Link>
            <Link
              to={`/register?next=${encodeURIComponent(`/invite/${token}`)}`}
              className="block text-center text-xs text-ink3 hover:text-ink"
            >
              No account yet? Create one
            </Link>
          </>
        ) : emailMismatch ? (
          <p
            role="alert"
            className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400"
          >
            This invite was issued to another email.
          </p>
        ) : (
          <Button fullWidth disabled={pending} onClick={() => void accept()}>
            Accept invite
          </Button>
        )}
      </Card>
    </div>
  )
}
