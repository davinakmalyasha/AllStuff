import { useEffect, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { api, type BusinessDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { PageSpinner } from '@/components/ui/Spinner'
import { toast } from '@/components/ui/Toast'

/** Allowlist editor (Batch 2): false-positive escapes for the banned-word filter. */
function AllowlistEditor() {
  const qc = useQueryClient()
  const [word, setWord] = useState('')
  const [words, setWords] = useState<string[]>([])
  const { data: saved, isLoading } = useQuery({
    queryKey: ['allowlist'],
    queryFn: () => api<{ words: string[] }>('/admin/banned-words/allowlist'),
  })
  useEffect(() => {
    if (saved) setWords(saved.words)
  }, [saved])

  const save = useMutation({
    mutationFn: () => api('/admin/banned-words/allowlist', { method: 'PUT', body: { words } }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['allowlist'] })
      toast.success('Allowlist saved')
    },
  })

  if (isLoading) return <p className="text-xs text-ink3">Loading…</p>

  return (
    <div className="space-y-2">
      <div className="flex gap-2">
        <input value={word} onChange={(e) => setWord(e.target.value)} placeholder="Add an allowed word…" className="h-9 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink" />
        <Button size="sm" onClick={() => { if (word.trim()) { setWords((p) => [...p, word.trim().toLowerCase()]); setWord('') } }} disabled={!word.trim()}>
          <Plus className="h-3.5 w-3.5" /> Add
        </Button>
      </div>
      <div className="flex flex-wrap gap-2">
        {words.map((w) => (
          <span key={w} className="flex items-center gap-1.5 rounded-full bg-surface2 px-2.5 py-1 text-xs text-ink2">
            {w}
            <button onClick={() => setWords((p) => p.filter((x) => x !== w))} className="text-ink3 hover:text-ink" aria-label={`Remove ${w}`}>
              <Trash2 className="h-3 w-3" />
            </button>
          </span>
        ))}
        {!words.length && <p className="text-xs text-ink3">Nothing allowed yet.</p>}
      </div>
      <Button size="sm" variant="secondary" onClick={() => void save.mutateAsync()} disabled={save.isPending}>Save allowlist</Button>
    </div>
  )
}

/** Curation & config (PRD §5.8.5): featured, banned words, announcement. */
export function AdminCurationPage() {
  const qc = useQueryClient()
  const [featured, setFeatured] = useState<string[]>([])
  const [word, setWord] = useState('')
  const [announcement, setAnnouncement] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['curation'],
    queryFn: async () => {
      const cfg = await api<{ featured_ids: string[]; leaderboard: Record<string, unknown> }>('/admin/curation')
      setFeatured(cfg.featured_ids)
      return cfg
    },
  })

  const { data: banned } = useQuery({
    queryKey: ['banned-words'],
    queryFn: () => api<{ words: string[] }>('/admin/banned-words'),
  })

  const { data: siteCfg } = useQuery({
    queryKey: ['site-config'],
    queryFn: () => api<Record<string, unknown>>('/admin/settings'),
  })
  useEffect(() => {
    if (siteCfg && typeof siteCfg.announcement === 'string') setAnnouncement(siteCfg.announcement)
  }, [siteCfg])

  const { data: search } = useQuery({
    queryKey: ['search-curation'],
    queryFn: () => api<{ businesses: BusinessDTO[] }>('/search?sort=rating&limit=10'),
  })

  const save = useMutation({
    mutationFn: () => api('/admin/curation', { method: 'PUT', body: { featured_ids: featured } }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['curation'] })
      toast.success('Featured saved')
    },
  })

  const addWord = useMutation({
    mutationFn: () => api('/admin/banned-words', { method: 'POST', body: { word } }),
    onSuccess: () => {
      setWord('')
      qc.invalidateQueries({ queryKey: ['banned-words'] })
    },
  })

  const removeWord = useMutation({
    mutationFn: (w: string) => api(`/admin/banned-words/${encodeURIComponent(w)}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['banned-words'] }),
  })

  const saveAnnouncement = useMutation({
    mutationFn: () => api('/admin/settings', { method: 'PUT', body: { announcement } }),
    onSuccess: () => {
      toast.success('Announcement saved')
      qc.invalidateQueries({ queryKey: ['site-config'] })
    },
  })

  if (isLoading) return <PageSpinner />

  return (
    <div className="mx-auto max-w-2xl space-y-8">
      <div>
        <p className="mono-label mb-1">Admin · Curation</p>
        <h1 className="text-2xl font-semibold tracking-tight">Homepage curation</h1>
        <p className="mt-1 text-sm text-ink2">Featured businesses appear on the homepage. Click to toggle (max 8).</p>
      </div>

      <div className="space-y-2">
        {search?.businesses.map((b) => {
          const on = featured.includes(b.id)
          return (
            <Card key={b.id} className="flex items-center gap-3">
              <button
                onClick={() =>
                  setFeatured((prev) =>
                    on ? prev.filter((x) => x !== b.id) : prev.length >= 8 ? prev : [...prev, b.id],
                  )
                }
                className={`flex h-6 w-6 items-center justify-center rounded-md border text-xs font-mono ${
                  on ? 'border-ink bg-accent text-accent-ink' : 'border-border text-ink3'
                }`}
              >
                {on ? '✓' : '+'}
              </button>
              <img src={b.logo_url ?? ''} alt="" className="h-10 w-10 rounded-lg object-cover" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-semibold text-ink">{b.name}</p>
                <p className="text-xs text-ink3">{b.category_name} · {b.city}</p>
              </div>
            </Card>
          )
        })}
      </div>

      <Button className="mt-2" onClick={() => void save.mutateAsync()} disabled={save.isPending}>Save featured</Button>

      <Card className="space-y-3">
        <p className="mono-label">Announcement banner</p>
        <input
          value={announcement}
          onChange={(e) => setAnnouncement(e.target.value)}
          placeholder="e.g. Welcome to BizVerse — 10,000 businesses and counting!"
          className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink"
        />
        <Button size="sm" onClick={() => void saveAnnouncement.mutateAsync()} disabled={saveAnnouncement.isPending}>Save announcement</Button>
      </Card>

      <Card className="space-y-3">
        <p className="mono-label">Banned words (chat & content, PRD §8.7)</p>
        <div className="flex gap-2">
          <input
            value={word}
            onChange={(e) => setWord(e.target.value)}
            placeholder="Add a blocked word…"
            className="h-9 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink"
          />
          <Button size="sm" onClick={() => void addWord.mutateAsync()} disabled={!word.trim()}>
            <Plus className="h-3.5 w-3.5" /> Add
          </Button>
        </div>
        <div className="flex flex-wrap gap-2">
          {banned?.words.map((w) => (
            <span key={w} className="flex items-center gap-1.5 rounded-full bg-surface2 px-2.5 py-1 text-xs text-ink2">
              {w}
              <button onClick={() => void removeWord.mutateAsync(w)} className="text-ink3 hover:text-ink" aria-label={`Remove ${w}`}>
                <Trash2 className="h-3 w-3" />
              </button>
            </span>
          ))}
          {!banned?.words.length && <p className="text-xs text-ink3">No banned words yet.</p>}
        </div>
      </Card>

      <Card className="space-y-3">
        <p className="mono-label">Allowlist (false-positive escapes, PRD E13)</p>
        <p className="text-xs text-ink2">Allowed words are ignored by the banned-word filter.</p>
        <AllowlistEditor />
      </Card>

      <Card className="space-y-2">
        <p className="mono-label">Leaderboard engine</p>
        <pre className="overflow-x-auto rounded-lg bg-surface2 p-3 text-xs text-ink2">
          {JSON.stringify(data?.leaderboard ?? {}, null, 2)}
        </pre>
      </Card>
    </div>
  )
}
