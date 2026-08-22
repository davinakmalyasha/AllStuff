import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bookmark, Heart, ThumbsUp } from 'lucide-react'
import { api, type CollectionDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { useAuth } from '@/stores/auth'

/**
 * Like / Recommend / Save action bar (PRD §5.6.1). Optimistic toggles,
 * server reconciliation, collection picker on save.
 */
export function EngagementBar({
  businessId,
  businessSlug,
  counts,
}: {
  businessId: string
  businessSlug: string
  counts: { likes: number; recommends: number; saves: number }
}) {
  const qc = useQueryClient()
  const { user } = useAuth()
  const [pickerOpen, setPickerOpen] = useState(false)
  const [creatingName, setCreatingName] = useState('')
  const [liked, setLiked] = useState(false)
  const [recommended, setRecommended] = useState(false)
  const [savedIn, setSavedIn] = useState<string[]>([])
  // Optimistic deltas: added while a toggle is in flight, cleared once the
  // server state arrives (prevents double-count while my-state is loading).
  const [likeDelta, setLikeDelta] = useState(0)
  const [recDelta, setRecDelta] = useState(0)

  const { data: myState } = useQuery({
    queryKey: ['my-state', businessId],
    queryFn: async () => {
      const [like, rec, saved] = await Promise.all([
        api<{ liked: boolean }>(`/likes/business/${businessId}`).catch(() => ({ liked: false })),
        api<{ recommended: boolean }>(`/recommends/${businessId}`).catch(() => ({ recommended: false })),
        api<{ collection_ids: string[] }>(`/saved/business/${businessId}`).catch(() => ({ collection_ids: [] })),
      ])
      return { liked: like.liked, recommended: rec.recommended, saved: saved.collection_ids }
    },
    enabled: !!user,
  })

  const { data: collections } = useQuery({
    queryKey: ['my-collections'],
    queryFn: () => api<{ collections: CollectionDTO[] }>('/me/collections'),
    enabled: pickerOpen && !!user,
  })

  const likeMut = useMutation({
    mutationFn: (on: boolean) => api(`/likes/business/${businessId}`, { method: on ? 'PUT' : 'DELETE' }),
    onMutate: (on) => {
      setLiked(on)
      setLikeDelta((d) => d + (on ? 1 : -1))
    },
  })
  const recMut = useMutation({
    mutationFn: (on: boolean) => api(`/recommends/${businessId}`, { method: on ? 'PUT' : 'DELETE' }),
    onMutate: (on) => {
      setRecommended(on)
      setRecDelta((d) => d + (on ? 1 : -1))
    },
  })
  const saveMut = useMutation({
    mutationFn: (collectionId: string) =>
      api(`/me/collections/items`, { method: 'POST', body: { target_type: 'business', target_id: businessId, collection_id: collectionId } }),
    onSuccess: () => {
      setSavedIn((prev) => [...prev, 'saved'])
      setPickerOpen(false)
      qc.invalidateQueries({ queryKey: ['my-state', businessId] })
    },
  })
  const createMut = useMutation({
    mutationFn: () => api<{ collection: CollectionDTO }>('/me/collections', { method: 'POST', body: { name: creatingName } }),
    onSuccess: (r) => {
      void saveMut.mutateAsync(r.collection.id)
      setCreatingName('')
      qc.invalidateQueries({ queryKey: ['my-collections'] })
    },
  })

  const isLiked = myState?.liked ?? liked
  const isRecommended = myState?.recommended ?? recommended
  const isSaved = (myState?.saved.length ?? 0) > 0 || savedIn.length > 0

  // Server truth arrived: drop the optimistic deltas.
  useEffect(() => {
    if (myState) {
      setLikeDelta(0)
      setRecDelta(0)
    }
  }, [myState?.liked, myState?.recommended])

  if (!user) {
    return (
      <div className="flex items-center gap-2">
        <a href={`/login?next=/b/${businessSlug}`}>
          <Button variant="secondary"><Heart className="h-4 w-4" /> {counts.likes}</Button>
        </a>
        <a href={`/login?next=/b/${businessSlug}`}>
          <Button variant="secondary"><ThumbsUp className="h-4 w-4" /> {counts.recommends}</Button>
        </a>
      </div>
    )
  }

  return (
    <div className="relative flex items-center gap-2">
      <Button
        variant={isLiked ? 'primary' : 'secondary'}
        onClick={() => void likeMut.mutateAsync(!isLiked)}
        disabled={likeMut.isPending}
      >
        <Heart className={`h-4 w-4 ${isLiked ? 'fill-current' : ''}`} /> {counts.likes + likeDelta}
      </Button>
      <Button
        variant={isRecommended ? 'primary' : 'secondary'}
        onClick={() => void recMut.mutateAsync(!isRecommended)}
        disabled={recMut.isPending}
      >
        <ThumbsUp className={`h-4 w-4 ${isRecommended ? 'fill-current' : ''}`} /> Recommend {counts.recommends + recDelta}
      </Button>
      <Button variant={isSaved ? 'primary' : 'secondary'} onClick={() => setPickerOpen((v) => !v)}>
        <Bookmark className={`h-4 w-4 ${isSaved ? 'fill-current' : ''}`} /> Save
      </Button>

      {pickerOpen && (
        <div className="absolute right-0 top-12 z-40 w-64 rounded-xl border border-border bg-surface p-3 shadow-cardHover">
          <p className="mono-label mb-2">Save to collection</p>
          <div className="max-h-48 space-y-1 overflow-y-auto">
            {collections?.collections.map((c) => (
              <button
                key={c.id}
                onClick={() => void saveMut.mutateAsync(c.id)}
                className="flex w-full items-center justify-between rounded-lg px-2 py-1.5 text-sm text-ink hover:bg-surface2"
              >
                <span>{c.name}</span>
                <span className="text-xs text-ink3">{c.item_count}</span>
              </button>
            ))}
          </div>
          <div className="mt-2 flex gap-2 border-t border-border pt-2">
            <input
              value={creatingName}
              onChange={(e) => setCreatingName(e.target.value)}
              placeholder="New collection…"
              className="h-8 flex-1 rounded border border-border bg-surface px-2 text-sm text-ink"
            />
            <Button size="sm" onClick={() => void createMut.mutateAsync()} disabled={!creatingName.trim() || createMut.isPending}>
              Create
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
