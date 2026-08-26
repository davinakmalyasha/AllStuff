import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { api, type CollectionDTO, type CollectionItemDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { PageSpinner } from '@/components/ui/Spinner'
import { Confirm } from '@/components/ui/Modal'
import { toast } from '@/components/ui/Toast'
import { usePageMeta } from '@/lib/meta'

export function CollectionsPage() {
  const qc = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [openId, setOpenId] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<CollectionDTO | null>(null)

  usePageMeta('My collections')

  const { data, isLoading } = useQuery({
    queryKey: ['my-collections'],
    queryFn: () => api<{ collections: CollectionDTO[] }>('/me/collections'),
  })

  const { data: itemsData } = useQuery({
    queryKey: ['collection-items', openId],
    queryFn: () => api<{ items: CollectionItemDTO[] }>(`/me/collections/${openId}/items`),
    enabled: !!openId,
  })

  const createMut = useMutation({
    mutationFn: () => api('/me/collections', { method: 'POST', body: { name } }),
    onSuccess: () => {
      setName('')
      setCreating(false)
      qc.invalidateQueries({ queryKey: ['my-collections'] })
      toast.success('Collection created')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not create the collection.'),
  })

  const togglePublic = useMutation({
    mutationFn: (c: CollectionDTO) => api(`/me/collections/${c.id}`, { method: 'PATCH', body: { is_public: !c.is_public } }),
    onSuccess: (_r, c) => {
      qc.invalidateQueries({ queryKey: ['my-collections'] })
      toast.success(c.is_public ? 'Collection is now private' : 'Collection is now public')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not update the collection.'),
  })

  const remove = useMutation({
    mutationFn: (c: CollectionDTO) => api(`/me/collections/${c.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['my-collections'] })
      toast.success('Collection deleted')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not delete the collection.'),
  })

  const removeItem = useMutation({
    mutationFn: (item: CollectionItemDTO) => api(`/me/collections/${openId}/items/${item.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['collection-items', openId] })
      toast.success('Item removed')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not remove the item.'),
  })

  if (isLoading) return <PageSpinner />

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Saved</p>
          <h1 className="text-2xl font-semibold tracking-tight">Collections</h1>
          <p className="mt-1 text-sm text-ink2">Organized lists — "Favorites" is created automatically.</p>
        </div>
        <Button onClick={() => setCreating(true)}><Plus className="h-4 w-4" /> New collection</Button>
      </div>

      {creating && (
        <Card className="mb-4 flex gap-2">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Collection name…"
            autoFocus
            className="h-10 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink"
          />
          <Button onClick={() => void createMut.mutateAsync()} disabled={!name.trim() || createMut.isPending}>Create</Button>
          <Button variant="secondary" onClick={() => setCreating(false)}>Cancel</Button>
        </Card>
      )}

      <div className="space-y-3">
        {data?.collections.map((c) => (
          <Card key={c.id} className="p-0">
            <div className="flex items-center gap-3 p-4">
              <button onClick={() => setOpenId(openId === c.id ? null : c.id)} className="flex-1 text-left">
                <p className="text-sm font-semibold text-ink">{c.name} <span className="text-xs font-normal text-ink3">({c.item_count})</span></p>
                <p className="text-xs text-ink3">/{c.slug}</p>
              </button>
              <Button variant="ghost" size="sm" onClick={() => void togglePublic.mutateAsync(c)} disabled={togglePublic.isPending}>
                {c.is_public ? 'Public' : 'Private'}
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setDeleteTarget(c)} aria-label="Delete collection">
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
            {openId === c.id && (
              <div className="border-t border-border px-4 py-3">
                {!itemsData?.items.length && <p className="text-sm text-ink3">Nothing saved here yet.</p>}
                {itemsData?.items.map((item) => (
                  <div key={item.id} className="flex items-center gap-2 py-1.5 text-sm">
                    {item.target_type === 'business' && item.target_slug ? (
                      <Link to={`/b/${item.target_slug}`} className="flex min-w-0 flex-1 items-center gap-2 text-ink hover:underline">
                        {item.target_logo ? (
                          <img src={item.target_logo} alt="" className="h-7 w-7 rounded-md object-cover" />
                        ) : (
                          <span className="flex h-7 w-7 items-center justify-center rounded-md bg-surface2 text-xs font-semibold">{(item.target_name ?? '?').charAt(0)}</span>
                        )}
                        <span className="truncate font-medium">{item.target_name}</span>
                      </Link>
                    ) : (
                      <span className="flex-1 truncate text-ink">{item.target_name || `${item.target_type} · ${item.target_id.slice(0, 8)}…`}</span>
                    )}
                    {item.note && <span className="text-xs text-ink3">{item.note}</span>}
                    <button onClick={() => void removeItem.mutateAsync(item)} className="text-ink3 hover:text-ink" aria-label="Remove item">
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </Card>
        ))}
        {!data?.collections.length && (
          <Card className="py-12 text-center text-sm text-ink3">No collections yet. Save a business to create your first one.</Card>
        )}
      </div>

      <Confirm
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && void remove.mutateAsync(deleteTarget)}
        title="Delete collection"
        message={`Delete "${deleteTarget?.name}"? Saved businesses stay in your account.`}
        confirmLabel="Delete"
        danger
      />
    </div>
  )
}
