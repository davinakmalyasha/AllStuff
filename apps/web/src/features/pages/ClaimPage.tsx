import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { api, type CategoryDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { Input } from '@/components/ui/Input'
import { usePageMeta } from '@/lib/meta'
import { toast } from '@/components/ui/Toast'

/** Claim a business (PRD §6.1): add an unlisted business or claim a listing. */
export function ClaimPage() {
  usePageMeta('Claim your business')
  const [form, setForm] = useState({
    name: '', category_id: '', address: '', city: '', country: '', website: '', evidence: '',
  })

  const { data: catData } = useQuery({
    queryKey: ['categories'],
    queryFn: () => api<{ categories: CategoryDTO[] }>('/categories'),
  })
  const leafCats: CategoryDTO[] = []
  const walk = (cs: CategoryDTO[]) => {
    for (const c of cs) {
      if (c.children?.length) walk(c.children)
      else leafCats.push(c)
    }
  }
  walk(catData?.categories ?? [])

  const submit = useMutation({
    mutationFn: () => api('/claims', { method: 'POST', body: form }),
    onSuccess: () => {
      toast.success('Claim submitted — our team reviews it shortly.')
      setForm({ name: '', category_id: '', address: '', city: '', country: '', website: '', evidence: '' })
    },
  })

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }))

  return (
    <div className="mx-auto max-w-2xl py-10">
      <p className="mono-label mb-1">Owners</p>
      <h1 className="text-2xl font-semibold tracking-tight">Claim your business</h1>
      <p className="mt-1 text-sm text-ink2">
        Is your business missing from the directory? Add it here — after review you'll get the owner dashboard
        with a free storefront, chat, and analytics.
      </p>

      <Card className="mt-6 space-y-4">
        <Input label="Business name" required value={form.name} onChange={set('name')} placeholder="e.g. Rumah Kopi Senja" />
        <div>
          <label className="mb-1.5 block text-sm font-medium text-ink">Category</label>
          <select value={form.category_id} onChange={set('category_id')} className="h-10 w-full rounded-lg border border-border bg-surface px-3 text-sm text-ink">
            <option value="">Pick a sub-category…</option>
            {leafCats.map((c) => (
              <option key={c.id} value={c.id}>{c.name}</option>
            ))}
          </select>
        </div>
        <Input label="Address" required value={form.address} onChange={set('address')} placeholder="Street, building, area" />
        <div className="grid grid-cols-2 gap-4">
          <Input label="City" required value={form.city} onChange={set('city')} />
          <Input label="Country" value={form.country} onChange={set('country')} />
        </div>
        <Input label="Website" value={form.website} onChange={set('website')} placeholder="https://…" />
        <div>
          <label className="mb-1.5 block text-sm font-medium text-ink">How are you connected to this business?</label>
          <textarea rows={3} value={form.evidence} onChange={set('evidence')} placeholder="e.g. I've managed the shop since 2019" className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink" />
        </div>
        {submit.error && <p className="text-sm text-red-600 dark:text-red-400">{(submit.error as Error).message}</p>}
        <div className="flex justify-end">
          <Button onClick={() => void submit.mutateAsync()} disabled={!form.name.trim() || !form.address.trim() || !form.city.trim() || submit.isPending}>
            Submit claim
          </Button>
        </div>
      </Card>
    </div>
  )
}
