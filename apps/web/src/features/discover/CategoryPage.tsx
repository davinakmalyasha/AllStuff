import { Link, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { BellPlus, BellRing } from 'lucide-react'
import { api, type BusinessDTO, type CategoryDTO, type TrendEntryDTO } from '@/lib/api'
import { ErrorNote, PageSpinner } from '@/components/ui/Spinner'
import { BusinessCard } from '@/components/ui/BusinessCard'
import { Button } from '@/components/ui/Button'
import { usePageMeta, useJsonLd } from '@/lib/meta'
import { useAuth } from '@/stores/auth'

export function CategoryPage() {
  const { slug = '' } = useParams()
  const { user } = useAuth()
  const qc = useQueryClient()

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['category', slug],
    queryFn: () =>
      api<{ category: CategoryDTO; breadcrumbs: CategoryDTO[]; leaderboard: TrendEntryDTO[]; updated_at: string }>(
        `/categories/${slug}`,
      ),
  })

  const cat = data?.category

  const { data: followState } = useQuery({
    queryKey: ['cat-follow', cat?.id],
    queryFn: () => api<{ following: boolean }>(`/categories/${cat!.id}/follow`),
    enabled: !!user && !!cat?.id,
  })
  const toggleFollow = useMutation({
    mutationFn: (on: boolean) =>
      api(`/categories/${cat!.id}/follow`, { method: on ? 'PUT' : 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['cat-follow', cat?.id] }),
  })
  usePageMeta(cat ? `${cat.name} — Businesses` : 'Category', cat?.description ?? undefined, {
    url: cat ? `${window.location.origin}/c/${cat.slug}` : undefined,
  })
  useJsonLd(
    cat
      ? { '@context': 'https://schema.org', '@type': 'CollectionPage', name: cat.name, description: cat.description ?? undefined }
      : null,
  )

  if (isLoading) return <PageSpinner />
  if (isError) return <ErrorNote onRetry={() => void refetch()} />
  if (!cat) {
    return (
      <div className="container-page flex min-h-[40vh] flex-col items-center justify-center gap-2 text-center">
        <p className="font-mono text-5xl font-semibold tracking-tight">404</p>
        <p className="text-sm text-ink2">Category not found.</p>
      </div>
    )
  }

  return (
    <div className="container-page py-10">
      <nav className="mb-2 flex items-center gap-1.5 text-xs text-ink3" aria-label="Breadcrumb">
        <Link to="/categories" className="hover:text-ink">Categories</Link>
        {data?.breadcrumbs.map((c) => (
          <span key={c.id} className="flex items-center gap-1.5">
            <span>/</span>
            <Link to={`/c/${c.slug}`} className={c.slug === slug ? 'text-ink' : 'hover:text-ink'}>{c.name}</Link>
          </span>
        ))}
      </nav>
      <h1 className="text-2xl font-semibold tracking-tight">{cat.name}</h1>
      {cat.description && <p className="mt-1 max-w-xl text-sm text-ink2">{cat.description}</p>}
      <div className="mt-2 flex flex-wrap items-center gap-3">
        <p className="text-xs text-ink3">{cat.count} verified business{cat.count === 1 ? '' : 'es'}</p>
        {user && cat && (
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void toggleFollow.mutateAsync(!followState?.following)}
            disabled={toggleFollow.isPending}
          >
            {followState?.following ? <BellRing className="h-3.5 w-3.5" /> : <BellPlus className="h-3.5 w-3.5" />}
            {followState?.following ? 'Following' : 'Follow'}
          </Button>
        )}
      </div>

      {data?.leaderboard.length ? (
        <section className="mt-10">
          <div className="mb-3 flex items-baseline gap-2">
            <p className="mono-label">Top in {cat.name}</p>
            {data.updated_at && (
              <span className="text-xs text-ink3">
                updated {Math.max(1, Math.round((Date.now() - new Date(data.updated_at).getTime()) / 60000))} min ago
              </span>
            )}
          </div>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {data.leaderboard.map((b, i) => (
              <div key={b.id} className="relative">
                <span className="absolute -left-2 -top-2 z-10 flex h-7 w-7 items-center justify-center rounded-full bg-accent font-mono text-xs font-semibold text-accent-ink">
                  {i + 1}
                </span>
                <BusinessCard business={b as unknown as BusinessDTO} />
                {(b as TrendEntryDTO).is_rising && (
                  <span className="absolute -right-2 -top-2 z-10 rounded-full border border-border bg-surface px-2 py-0.5 text-[10px] font-semibold text-ink2">
                    ▲ Rising
                  </span>
                )}
              </div>
            ))}
          </div>
        </section>
      ) : (
        <p className="mt-10 text-sm text-ink3">No verified businesses in this category yet. Be the first to register yours.</p>
      )}
    </div>
  )
}
