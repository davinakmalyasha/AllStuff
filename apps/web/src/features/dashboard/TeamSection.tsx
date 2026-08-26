import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trash2, UserPlus } from 'lucide-react'
import { api } from '@/lib/api'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { Input } from '@/components/ui/Input'
import { Confirm } from '@/components/ui/Modal'
import { toast } from '@/components/ui/Toast'

interface InviteDTO {
  id: string
  business_id: string
  email: string
  role: string
  created_at: string
  accepted_at: string | null
  expires_at: string
  revoked_at: string | null
}

/** Co-owner invitations (PRD §5.9.3). */
export function TeamSection() {
  const qc = useQueryClient()
  const business = useActiveBusiness()
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<'co_owner' | 'viewer'>('co_owner')
  const [flash, setFlash] = useState('')
  const [revokeTarget, setRevokeTarget] = useState<InviteDTO | null>(null)

  const { data } = useQuery({
    queryKey: ['invites', business?.id],
    queryFn: () => api<{ invites: InviteDTO[] }>(`/businesses/${business!.id}/invites`),
    enabled: !!business,
  })

  const invite = useMutation({
    mutationFn: () => api(`/businesses/${business!.id}/invites`, { method: 'POST', body: { email, role } }),
    onSuccess: () => {
      setEmail('')
      setFlash('Invite sent')
      setTimeout(() => setFlash(''), 2000)
      qc.invalidateQueries({ queryKey: ['invites', business?.id] })
    },
  })

  const revoke = useMutation({
    mutationFn: (id: string) => api(`/businesses/${business!.id}/invites/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['invites', business?.id] })
      toast.success('Invite revoked')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not revoke the invite.'),
  })

  if (!business) return null

  return (
    <Card className="space-y-4">
      <p className="mono-label">Team & co-owners</p>
      <p className="text-sm text-ink2">
        Invite people to help manage this business. Invitees get an email with an accept link. Co-owners get full
        management access; viewers get read-only access to analytics.
      </p>
      <div className="flex items-end gap-2">
        <Input label="Email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="teammate@example.com" />
        <select value={role} onChange={(e) => setRole(e.target.value as 'co_owner' | 'viewer')} className="h-10 rounded-lg border border-border bg-surface px-3 text-sm text-ink">
          <option value="co_owner">Co-owner</option>
          <option value="viewer">Viewer</option>
        </select>
        <Button onClick={() => void invite.mutateAsync()} disabled={!email.includes('@') || invite.isPending}>
          <UserPlus className="h-4 w-4" /> Invite
        </Button>
      </div>
      {flash && <p className="text-sm text-ink2">{flash}</p>}
      {invite.error && <p className="text-sm text-red-600 dark:text-red-400">{(invite.error as Error).message}</p>}
      <div className="space-y-1.5">
        {data?.invites.map((i) => (
          <div key={i.id} className="flex items-center gap-2 rounded-lg bg-surface2 px-3 py-2 text-sm">
            <span className="min-w-0 flex-1 truncate text-ink">{i.email}</span>
            <Badge>{i.role}</Badge>
            {i.accepted_at ? (
              <span className="text-xs text-ink3">accepted</span>
            ) : i.revoked_at ? (
              <span className="text-xs text-ink3">revoked</span>
            ) : (
              <>
                <span className="text-xs text-ink3">pending</span>
                <button onClick={() => setRevokeTarget(i)} className="text-ink3 hover:text-ink" aria-label="Revoke invite">
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </>
            )}
          </div>
        ))}
        {(data?.invites.length ?? 0) === 0 && <p className="text-xs text-ink3">No invites yet.</p>}
      </div>

      <Confirm
        open={!!revokeTarget}
        onClose={() => setRevokeTarget(null)}
        onConfirm={() => revokeTarget && void revoke.mutateAsync(revokeTarget.id)}
        title="Revoke invite"
        message={`Revoke the pending invite for ${revokeTarget?.email}? The link will stop working.`}
        confirmLabel="Revoke"
        danger
      />
    </Card>
  )
}
