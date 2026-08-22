import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { PageSpinner } from '@/components/ui/Spinner'

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

/** User management (PRD §5.8.4). */
export function AdminUsersPage() {
  const qc = useQueryClient()
  const [q, setQ] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['admin-users', q],
    queryFn: () => api<{ users: AdminUserDTO[] }>(`/admin/users?q=${encodeURIComponent(q || '%')}&limit=30`),
  })

  const action = useMutation({
    mutationFn: ({ id, action }: { id: string; action: string }) =>
      api(`/admin/users/${id}/${action}`, { method: 'POST', body: { reason: 'admin action' } }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['admin-users'] }),
  })

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
              <p className="text-xs text-ink3">{u.email} · joined {new Date(u.created_at).toLocaleDateString()}</p>
            </div>
            <Badge tone={u.status === 'active' ? 'positive' : u.status === 'banned' ? 'danger' : 'attention'} dot>{u.status}</Badge>
            {u.role === 'admin' && <Badge>Admin</Badge>}
            {u.status === 'active' ? (
              <div className="flex gap-1">
                <Button variant="secondary" size="sm" onClick={() => void action.mutateAsync({ id: u.id, action: 'warn' })}>Warn</Button>
                <Button variant="secondary" size="sm" onClick={() => void action.mutateAsync({ id: u.id, action: 'suspend' })}>Suspend</Button>
                <Button variant="danger" size="sm" onClick={() => void action.mutateAsync({ id: u.id, action: 'ban' })}>Ban</Button>
              </div>
            ) : (
              <Button variant="secondary" size="sm" onClick={() => void action.mutateAsync({ id: u.id, action: 'unban' })}>Restore</Button>
            )}
          </Card>
        ))}
      </div>
    </div>
  )
}
