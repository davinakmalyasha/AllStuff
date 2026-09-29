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
export function AdminCurationPage() {  const qc = useQueryClient()
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
    queryFn: () => api<{ businesses: BusinessDTO[] }>('/search?sort=rating&limit=10&with_total=0'),
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
      toast.success('Word blocked')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not add the word.'),
  })

  const removeWord = useMutation({
    mutationFn: (w: string) => api(`/admin/banned-words/${encodeURIComponent(w)}`, { method: 'DELETE' }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['banned-words'] })
      toast.success('Word removed')
    },
    onError: (e) => toast.error((e as Error).message || 'Could not remove the word.'),
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
                // aria-pressed exposes the toggle state: the glyph and the
                // background colour alone conveyed it to sighted users only, so
                // this control was invisible as a toggle to a screen reader.
                aria-pressed={on}
                aria-label={`${on ? 'Remove' : 'Add'} ${b.name} ${on ? 'from' : 'to'} featured`}
                className={`flex h-6 w-6 items-center justify-center rounded-md border text-xs font-mono ${
                  on ? 'border-ink bg-accent text-accent-ink' : 'border-border text-ink3'
                }`}
              >
                <span aria-hidden>{on ? '✓' : '+'}</span>
              </button>
              {b.logo_url ? (
                <img src={b.logo_url} alt="" className="h-10 w-10 rounded-lg object-cover" />
              ) : (
                <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-surface2 text-sm font-semibold">{b.name.charAt(0)}</div>
              )}
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

      <TrendingConfigCard />

      <Card className="space-y-3">
        <p className="mono-label">Banned words (chat & content)</p>
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
        <p className="mono-label">Allowlist (false-positive escapes)</p>
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

interface TrendingCfg {
  lambda_24h: number
  lambda_7d: number
  lambda_30d: number
  booming_n: number
  rising_n: number
  rising_paused: boolean
}

const TRENDING_DEFAULTS: TrendingCfg = {
  lambda_24h: 0.03,
  lambda_7d: 0.006,
  lambda_30d: 0.002,
  booming_n: 25,
  rising_n: 20,
  rising_paused: false,
}

/** Leaderboard tuning (PRD §5.8.5): decay rates, list sizes, Rising pause.
 *  Stored in site_config key "trending"; the engine picks it up next run. */
function TrendingConfigCard() {
  const qc = useQueryClient()
  const { data: site } = useQuery({
    queryKey: ['admin-settings'],
    queryFn: () => api<Record<string, unknown>>('/admin/settings'),
  })
  const [cfg, setCfg] = useState<TrendingCfg>(TRENDING_DEFAULTS)
  const [dirty, setDirty] = useState(false)
  useEffect(() => {
    if (!site) return
    const t = site.trending as Partial<TrendingCfg> | undefined
    if (t && typeof t === 'object') setCfg({ ...TRENDING_DEFAULTS, ...t })
  }, [site])

  const save = useMutation({
    mutationFn: () => api('/admin/settings', { method: 'PUT', body: { trending: cfg } }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin-settings'] })
      setDirty(false)
      toast.success('Trending config saved — applies on the next recompute')
    },
  })

  const numField = (key: keyof TrendingCfg, label: string, step: number) => (
    <label className="flex items-center justify-between gap-3 text-sm text-ink2">
      {label}
      <input
        type="number"
        step={step}
        value={cfg[key] as number}
        onChange={(e) => {
          setCfg((c) => ({ ...c, [key]: Number(e.target.value) }))
          setDirty(true)
        }}
        className="h-9 w-28 rounded-lg border border-border bg-surface px-3 text-sm text-ink"
      />
    </label>
  )

  return (
    <Card className="space-y-3">
      <p className="mono-label">Trending engine</p>
      {numField('lambda_24h', '24h decay λ', 0.001)}
      {numField('lambda_7d', '7d decay λ', 0.001)}
      {numField('lambda_30d', '30d decay λ', 0.001)}
      {numField('booming_n', 'Booming list size', 1)}
      {numField('rising_n', 'Rising list size', 1)}
      <label className="flex items-center justify-between gap-3 text-sm text-ink2">
        Pause Rising strip (freezes current badges)
        <input
          type="checkbox"
          checked={cfg.rising_paused}
          onChange={(e) => {
            setCfg((c) => ({ ...c, rising_paused: e.target.checked }))
            setDirty(true)
          }}
          className="h-4 w-4 accent-black dark:accent-white"
        />
      </label>
      <Button size="sm" onClick={() => void save.mutateAsync()} disabled={!dirty || save.isPending}>Save trending config</Button>
    </Card>
  )
}
