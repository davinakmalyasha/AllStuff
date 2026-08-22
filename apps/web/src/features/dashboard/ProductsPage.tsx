import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, Loader2, Pencil, Plus, Trash2 } from 'lucide-react'
import { api, uploadMedia, type ProductDTO, type ProductVariantDTO } from '@/lib/api'
import { useActiveBusiness } from '@/app/shells/OwnerShell'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Badge } from '@/components/ui/Badge'
import { Input } from '@/components/ui/Input'
import { PageSpinner } from '@/components/ui/Spinner'
import { priceLabel } from './StorefrontPreview'

export function ProductsPage() {
  const qc = useQueryClient()
  const business = useActiveBusiness()
  const [editing, setEditing] = useState<ProductDTO | null>(null)
  const [creating, setCreating] = useState(false)

  const { data, isLoading } = useQuery({
    queryKey: ['products', business?.id],
    queryFn: () => api<{ products: ProductDTO[] }>(`/businesses/${business!.id}/products`),
    enabled: !!business,
  })

  const refresh = () => qc.invalidateQueries({ queryKey: ['products', business?.id] })

  const togglePublish = useMutation({
    mutationFn: (p: ProductDTO) =>
      api(`/products/${p.id}/${p.is_published ? 'unpublish' : 'publish'}`, { method: 'POST' }),
    onSuccess: refresh,
  })

  const duplicate = useMutation({
    mutationFn: (p: ProductDTO) => api(`/products/${p.id}/duplicate`, { method: 'POST' }),
    onSuccess: refresh,
  })

  const remove = useMutation({
    mutationFn: (p: ProductDTO) => api(`/products/${p.id}`, { method: 'DELETE' }),
    onSuccess: refresh,
  })

  if (!business) {
    return (
      <div className="mx-auto max-w-xl">
        <h1 className="text-2xl font-semibold tracking-tight">No business selected</h1>
        <p className="mt-2 text-sm text-ink2">Select a business to manage its catalog.</p>
      </div>
    )
  }
  if (isLoading) return <PageSpinner />

  return (
    <div className="mx-auto max-w-4xl">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Catalog</p>
          <h1 className="text-2xl font-semibold tracking-tight">Products & services</h1>
          <p className="mt-1 text-sm text-ink2">Drafts are private; publish when ready (PRD §5.4.3).</p>
        </div>
        <Button onClick={() => setCreating(true)}><Plus className="h-4 w-4" /> New product</Button>
      </div>

      <div className="space-y-3">
        {data?.products.map((p) => (
          <Card key={p.id} className="flex items-center gap-4">
            {p.cover_image_id ? (
              <img src={`/api/v1/media/${p.cover_image_id}/file`} alt="" className="h-14 w-14 shrink-0 rounded-lg object-cover" />
            ) : (
              <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-lg bg-surface2 text-lg font-semibold text-ink2">{p.name.charAt(0)}</div>
            )}
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <p className="truncate text-sm font-semibold text-ink">{p.name}</p>
                {p.badge !== 'none' && <Badge tone="attention">{p.badge}</Badge>}
                <Badge tone={p.is_published ? 'positive' : 'neutral'} dot>{p.is_published ? 'Live' : 'Draft'}</Badge>
              </div>
              <p className="mt-0.5 text-xs text-ink3">
                {p.type} · {priceLabel(p)}
                {p.variants && p.variants.length > 0 && ` · ${p.variants.length} variants`}
                {!p.is_available && ' · unavailable'}
              </p>
            </div>
            <div className="flex shrink-0 gap-1">
              <Button variant="ghost" size="sm" onClick={() => void togglePublish.mutateAsync(p)} aria-label="Toggle publish">
                {togglePublish.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : p.is_published ? 'Unpublish' : 'Publish'}
              </Button>
              <Button variant="ghost" size="sm" onClick={() => void duplicate.mutateAsync(p)} aria-label="Duplicate">
                <Copy className="h-3.5 w-3.5" />
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setEditing(p)} aria-label="Edit">
                <Pencil className="h-3.5 w-3.5" />
              </Button>
              <Button variant="ghost" size="sm" onClick={() => { if (window.confirm(`Delete "${p.name}"?`)) void remove.mutateAsync(p) }} aria-label="Delete">
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </div>
          </Card>
        ))}
        {!data?.products.length && (
          <Card className="py-12 text-center text-sm text-ink3">No products yet. Create your first one.</Card>
        )}
      </div>

      {(editing || creating) && (
        <ProductEditor
          businessId={business.id}
          product={editing}
          onClose={() => { setEditing(null); setCreating(false) }}
          onSaved={refresh}
        />
      )}
    </div>
  )
}

function ProductEditor({
  businessId,
  product,
  onClose,
  onSaved,
}: {
  businessId: string
  product: ProductDTO | null
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(product?.name ?? '')
  const [type, setType] = useState<'product' | 'service'>(product?.type ?? 'product')
  const [description, setDescription] = useState(product?.description ?? '')
  const [currency, setCurrency] = useState(product?.currency ?? 'USD')
  const [basePrice, setBasePrice] = useState(product?.base_price?.toString() ?? '')
  const [callForPrice, setCallForPrice] = useState(product?.call_for_price ?? false)
  const [tags, setTags] = useState(product?.tags.join(', ') ?? '')
  const [badge, setBadge] = useState(product?.badge ?? 'none')
  const [isAvailable, setIsAvailable] = useState(product?.is_available ?? true)
  const [isFeatured, setIsFeatured] = useState(product?.is_featured ?? false)
  const [imageIds, setImageIds] = useState<string[]>(product?.image_ids ?? [])
  const [options, setOptions] = useState<{ name: string; values: string[] }[]>(
    product?.options?.map((o) => ({ name: o.name, values: [...o.values] })) ?? [],
  )
  const [variants, setVariants] = useState<ProductVariantDTO[]>(product?.variants ?? [])
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [uploading, setUploading] = useState(false)

  const create = async () => {
    const r = await api<{ product: ProductDTO }>(`/businesses/${businessId}/products`, {
      method: 'POST',
      body: { name, type, currency, base_price: basePrice ? Number(basePrice) : null, description },
    })
    return r.product
  }

  const save = async () => {
    setSaving(true)
    setError('')
    try {
      let p = product
      if (!p) p = await create()
      await api(`/products/${p.id}`, {
        method: 'PATCH',
        body: {
          name, type, description, currency,
          base_price: basePrice ? Number(basePrice) : null,
          call_for_price: callForPrice,
          tags: tags.split(',').map((t) => t.trim()).filter(Boolean),
          badge, is_available: isAvailable, is_featured: isFeatured,
          image_ids: imageIds,
          cover_image_id: imageIds[0] ?? null,
        },
      })
      await api(`/products/${p.id}/variants`, {
        method: 'PUT',
        body: {
          options,
          variants: variants.map((v) => ({
            id: v.id, name: v.name, sku: v.sku, options: v.options,
            price: v.price, currency: v.currency || currency, stock_qty: v.stock_qty,
            in_stock: v.in_stock, image_id: v.image_id,
          })),
        },
      })
      onSaved()
      onClose()
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const uploadImage = async (file: File) => {
    setUploading(true)
    try {
      const r = await uploadMedia('product', file)
      setImageIds((prev) => [...prev, r.media.id])
    } finally {
      setUploading(false)
    }
  }

  const addOption = () => setOptions((prev) => [...prev, { name: '', values: [] }])

  const generateVariants = () => {
    const combos = combine(options.map((o) => o.values))
    setVariants(
      combos.map((combo, i) => ({
        id: `new-${i}`, product_id: product?.id ?? '', name: combo.join(' / '),
        sku: combo.join('-').toLowerCase().replace(/\s+/g, '-') || `v${i + 1}`,
        options: Object.fromEntries(options.map((o, j) => [o.name, combo[j] ?? ''])),
        price: basePrice ? Number(basePrice) : null, currency,
        stock_qty: null, in_stock: true, image_id: null, sort_order: i,
      })),
    )
  }

  const patchVariant = (i: number, field: keyof ProductVariantDTO, value: unknown) =>
    setVariants((prev) => prev.map((v, idx) => (idx === i ? { ...v, [field]: value } : v)))

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 backdrop-blur-sm" onClick={onClose}>
      <Card className="my-6 w-full max-w-3xl" >
        <div onClick={(e) => e.stopPropagation()} className="space-y-5">
          <div className="flex items-start justify-between">
            <div>
              <h2 className="text-lg font-semibold tracking-tight">{product ? 'Edit product' : 'New product'}</h2>
              <p className="text-xs text-ink3">Variants, pricing, stock — the full catalog model (PRD §5.4.3).</p>
            </div>
            <button onClick={onClose} className="rounded-lg p-1 text-ink3 hover:bg-surface2 hover:text-ink" aria-label="Close">✕</button>
          </div>

          {error && <p className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">{error}</p>}

          <div className="grid gap-4 sm:grid-cols-2">
            <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} required />
            <div>
              <label className="mb-1.5 block text-sm font-medium text-ink">Type</label>
              <select value={type} onChange={(e) => setType(e.target.value as 'product' | 'service')} className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink">
                <option value="product">Product</option>
                <option value="service">Service</option>
              </select>
            </div>
            <Input label="Base price" type="number" step="0.01" min="0" placeholder="0.00" value={basePrice} onChange={(e) => setBasePrice(e.target.value)} />
            <div className="grid grid-cols-2 gap-4">
              <Input label="Currency" value={currency} onChange={(e) => setCurrency(e.target.value.toUpperCase())} maxLength={3} />
              <div>
                <label className="mb-1.5 block text-sm font-medium text-ink">Badge</label>
                <select value={badge} onChange={(e) => setBadge(e.target.value as 'none' | 'new' | 'popular')} className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink">
                  <option value="none">None</option>
                  <option value="new">New</option>
                  <option value="popular">Popular</option>
                </select>
              </div>
            </div>
          </div>
          {type === 'service' && (
            <label className="flex items-center gap-2 text-sm text-ink2">
              <input type="checkbox" checked={callForPrice} onChange={(e) => setCallForPrice(e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" />
              Call for price
            </label>
          )}
          <div>
            <label className="mb-1.5 block text-sm font-medium text-ink">Description</label>
            <textarea rows={3} value={description} onChange={(e) => setDescription(e.target.value)} className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink" />
          </div>
          <Input label="Tags (comma separated)" value={tags} onChange={(e) => setTags(e.target.value)} hint="Max 5" />
          <div className="flex gap-4">
            <label className="flex items-center gap-2 text-sm text-ink2">
              <input type="checkbox" checked={isAvailable} onChange={(e) => setIsAvailable(e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" /> Available
            </label>
            <label className="flex items-center gap-2 text-sm text-ink2">
              <input type="checkbox" checked={isFeatured} onChange={(e) => setIsFeatured(e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" /> Featured
            </label>
          </div>

          {/* Images */}
          <div>
            <p className="mono-label mb-2">Images (first = cover)</p>
            <div className="flex flex-wrap gap-2">
              {imageIds.map((id) => (
                <div key={id} className="relative">
                  <img src={`/api/v1/media/${id}/file`} alt="" className="h-16 w-16 rounded-lg object-cover" />
                  <button onClick={() => setImageIds((prev) => prev.filter((x) => x !== id))} className="absolute -right-1.5 -top-1.5 rounded-full bg-accent p-0.5 text-accent-ink" aria-label="Remove image">✕</button>
                </div>
              ))}
              <label className="flex h-16 w-16 cursor-pointer items-center justify-center rounded-lg border border-dashed border-border text-ink3 hover:bg-surface2">
                {uploading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
                <input type="file" accept="image/jpeg,image/png,image/webp" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) void uploadImage(f) }} />
              </label>
            </div>
          </div>

          {/* Variants */}
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <p className="mono-label">Variants & options</p>
              <div className="flex gap-2">
                <Button variant="secondary" size="sm" onClick={addOption}>+ Option group</Button>
                <Button variant="secondary" size="sm" onClick={generateVariants} disabled={options.length === 0}>Generate combinations</Button>
              </div>
            </div>
            {options.map((o, i) => (
              <div key={i} className="flex items-center gap-2 rounded-lg border border-border p-2.5">
                <input value={o.name} onChange={(e) => setOptions((prev) => prev.map((x, idx) => idx === i ? { ...x, name: e.target.value } : x))} placeholder="Option name (e.g. Size)" className="h-8 w-32 rounded border border-border bg-surface px-2 text-sm text-ink" />
                <input value={o.values.join(', ')} onChange={(e) => setOptions((prev) => prev.map((x, idx) => idx === i ? { ...x, values: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) } : x))} placeholder="Values, comma separated (S, M, L)" className="h-8 flex-1 rounded border border-border bg-surface px-2 text-sm text-ink" />
                <button onClick={() => setOptions((prev) => prev.filter((_, idx) => idx !== i))} className="text-ink3 hover:text-ink" aria-label="Remove option">✕</button>
              </div>
            ))}
            {variants.length > 0 && (
              <div className="overflow-x-auto rounded-lg border border-border">
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b border-border text-left text-xs text-ink3">
                      <th className="px-3 py-2">Name</th>
                      <th className="px-3 py-2">SKU</th>
                      <th className="px-3 py-2">Price</th>
                      <th className="px-3 py-2">Stock</th>
                      <th className="px-3 py-2">In stock</th>
                      <th className="px-3 py-2"></th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {variants.map((v, i) => (
                      <tr key={v.id}>
                        <td className="px-3 py-1.5">
                          <input value={v.name} onChange={(e) => patchVariant(i, 'name', e.target.value)} className="h-8 w-full rounded border border-border bg-surface px-2 text-ink" />
                        </td>
                        <td className="px-3 py-1.5">
                          <input value={v.sku} onChange={(e) => patchVariant(i, 'sku', e.target.value)} className="h-8 w-28 rounded border border-border bg-surface px-2 font-mono text-xs text-ink" />
                        </td>
                        <td className="px-3 py-1.5">
                          <input type="number" step="0.01" min="0" value={v.price ?? ''} onChange={(e) => patchVariant(i, 'price', e.target.value ? Number(e.target.value) : null)} className="h-8 w-24 rounded border border-border bg-surface px-2 text-ink" />
                        </td>
                        <td className="px-3 py-1.5">
                          <input type="number" min="0" value={v.stock_qty ?? ''} onChange={(e) => patchVariant(i, 'stock_qty', e.target.value ? Number(e.target.value) : null)} className="h-8 w-16 rounded border border-border bg-surface px-2 text-ink" />
                        </td>
                        <td className="px-3 py-1.5">
                          <input type="checkbox" checked={v.in_stock} onChange={(e) => patchVariant(i, 'in_stock', e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" />
                        </td>
                        <td className="px-3 py-1.5">
                          <button onClick={() => setVariants((prev) => prev.filter((_, idx) => idx !== i))} className="text-ink3 hover:text-ink" aria-label="Remove variant">✕</button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {options.length === 0 && variants.length === 0 && (
              <p className="text-xs text-ink3">No variants — the base price applies (PRD §8.3).</p>
            )}
          </div>

          <div className="flex justify-end gap-2 border-t border-border pt-4">
            <Button variant="secondary" onClick={onClose}>Cancel</Button>
            <Button onClick={() => void save()} disabled={saving || !name.trim()}>
              {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Save'}
            </Button>
          </div>
        </div>
      </Card>
    </div>
  )
}

function combine(groups: string[][]): string[][] {
  if (groups.length === 0) return []
  return groups.reduce<string[][]>(
    (acc, group) => (group.length === 0 ? acc : acc.flatMap((combo) => group.map((v) => [...combo, v]))),
    [[]],
  )
}
