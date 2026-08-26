import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { useDebouncedValue } from '@/lib/hooks'
import { formatDate } from '@/lib/format'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner } from '@/components/ui/Spinner'
import { Confirm } from '@/components/ui/Modal'
import { toast } from '@/components/ui/Toast'

interface AdminUserDTO {
  id: string
  name: string
  email: string
  username: string
  status: string
  role: string
  email_verified: boolean
  created_at: string
}

type UserAction = 'warn' | 'suspend' | 'ban' | 'unban'

/** User management (PRD §5.8.4). */
export function AdminUsersPage() {
  const qc = useQueryClient()
  const [q, setQ] = useState('')
  const debouncedQ = useDebouncedValue(q, 250)
  // Suspend/ban are destructive — routed through a Confirm dialog first.
  const [confirmAction, setConfirmAction] = useState<{ user: AdminUserDTO; action: 'suspend' | 'ban' } | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['admin-users', debouncedQ],
    queryFn: () => api<{ users: AdminUserDTO[] }>(`/admin/users?q=${encodeURIComponent(debouncedQ || '%')}&limit=30`),
  })

  const action = useMutation({
    mutationFn: ({ id, action }: { id: string; action: UserAction }) =>
      api(`/admin/users/${id}/${action}`, { method: 'POST', body: { reason: 'admin action' } }),
    onSuccess: (_r, vars) => {
      qc.invalidateQueries({ queryKey: ['admin-users'] })
      toast.success(
        vars.action === 'warn' ? 'User warned'
          : vars.action === 'suspend' ? 'User suspended'
          : vars.action === 'ban' ? 'User banned'
          : 'User restored',
      )
    },
    onError: (e) => toast.error((e as Error).message || 'Action failed.'),
  })

  const runAction = (u: AdminUserDTO, act: UserAction) => {
    if (act === 'suspend' || act === 'ban') setConfirmAction({ user: u, action: act })
    else void action.mutateAsync({ id: u.id, action: act })
  }

  if (isLoading) return <PageSpinner />

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6">
        <p className="mono-label mb-1">Admin · Users</p>
        <h1 className="text-2xl font-semibold tracking-tight">Users</h1>
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search by name, email, or username…"
          className="mt-3 h-10 w-full max-w-md rounded-lg border border-border bg-surface px-3 text-sm text-ink"
        />
      </div>
      <div className="space-y-2">
        {data?.users.map((u) => (
          <Card key={u.id} className="flex items-center gap-3">
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-semibold text-ink">{u.name} <span className="font-normal text-ink3">@{u.username}</span></p>
              <p className="text-xs text-ink3">{u.email} · joined {formatDate(u.created_at)}</p>
            </div>
            <Badge tone={u.status === 'active' ? 'positive' : u.status === 'banned' ? 'danger' : 'attention'} dot>{u.status}</Badge>
            {u.role === 'admin' && <Badge>Admin</Badge>}
            {u.status === 'active' ? (
              <div className="flex gap-1">
                <Button variant="secondary" size="sm" onClick={() => runAction(u, 'warn')}>Warn</Button>
                <Button variant="secondary" size="sm" onClick={() => runAction(u, 'suspend')}>Suspend</Button>
                <Button variant="danger" size="sm" onClick={() => runAction(u, 'ban')}>Ban</Button>
              </div>
            ) : (
              <Button variant="secondary" size="sm" onClick={() => runAction(u, 'unban')}>Restore</Button>
            )}
          </Card>
        ))}
      </div>

      <Confirm
        open={!!confirmAction}
        onClose={() => setConfirmAction(null)}
        onConfirm={() =>
          confirmAction && void action.mutateAsync({ id: confirmAction.user.id, action: confirmAction.action })
        }
        title={confirmAction?.action === 'ban' ? 'Ban user' : 'Suspend user'}
        message={
          confirmAction?.action === 'ban'
            ? `Ban ${confirmAction.user.name} permanently? They lose access to BizVerse.`
            : `Suspend ${confirmAction?.user.name} for 7 days? They cannot sign in until it is lifted or they appeal.`
        }
        confirmLabel={confirmAction?.action === 'ban' ? 'Ban' : 'Suspend'}
        danger
      />
    </div>
  )
}
