import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, Plus, Pencil, Trash2 } from 'lucide-react'
import { api, type CategoryDTO } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Card } from '@/components/ui/Card'
import { PageSpinner } from '@/components/ui/Spinner'

type TreeNode = CategoryDTO

export function AdminCategoriesPage() {
  const qc = useQueryClient()
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [editing, setEditing] = useState<CategoryDTO | null>(null)
  const [creating, setCreating] = useState<{ parentId: string | null } | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['categories'],
    queryFn: () => api<{ categories: TreeNode[] }>('/categories'),
  })

  const refresh = () => qc.invalidateQueries({ queryKey: ['categories'] })

  const saveMutation = useMutation({
    mutationFn: (body: { name: string; parent_id?: string | null; slug?: string }) =>
      editing
        ? api(`/admin/categories/${editing.id}`, { method: 'PATCH', body })
        : api('/admin/categories', { method: 'POST', body }),
    onSuccess: () => {
      setEditing(null)
      setCreating(null)
      refresh()
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (c: CategoryDTO) => api(`/admin/categories/${c.id}`, { method: 'DELETE' }),
    onSuccess: refresh,
  })

  const toggle = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  const renderNode = (c: CategoryDTO, depth: number) => {
    const hasChildren = (c.children?.length ?? 0) > 0
    const isOpen = expanded.has(c.id)
    return (
      <div key={c.id}>
        <div
          className="group flex items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface2"
          style={{ paddingLeft: depth * 20 + 8 }}
        >
          <button
            onClick={() => hasChildren && toggle(c.id)}
            className="text-ink3 disabled:invisible"
            disabled={!hasChildren}
            aria-label={isOpen ? 'Collapse' : 'Expand'}
          >
            {hasChildren ? isOpen ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" /> : <span className="inline-block w-4" />}
          </button>
          <span className="flex-1 text-sm">
            <span className="font-medium text-ink">{c.name}</span>
            <span className="ml-2 font-mono text-xs text-ink3">/{c.slug}</span>
            {c.count > 0 && (
              <span className="ml-2 rounded-full bg-surface2 px-2 py-0.5 text-xs text-ink2">{c.count}</span>
            )}
          </span>
          <div className="flex gap-1 opacity-0 transition-opacity group-hover:opacity-100">
            <Button variant="ghost" size="sm" onClick={() => setCreating({ parentId: c.id })} aria-label={`Add child under ${c.name}`}>
              <Plus className="h-3.5 w-3.5" />
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setEditing(c)} aria-label={`Edit ${c.name}`}>
              <Pencil className="h-3.5 w-3.5" />
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                if (window.confirm(`Delete "${c.name}"?`)) void deleteMutation.mutateAsync(c)
              }}
              aria-label={`Delete ${c.name}`}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
        {hasChildren && isOpen && (
          <div>{c.children!.map((child) => renderNode(child, depth + 1))}</div>
        )}
      </div>
    )
  }

  const form = editing ?? (creating ? ({ id: '', name: '', parent_id: creating.parentId } as CategoryDTO) : null)

  return (
    <div className="mx-auto max-w-3xl">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <p className="mono-label mb-1">Admin · Directory</p>
          <h1 className="text-2xl font-semibold tracking-tight">Categories</h1>
          <p className="mt-1 text-sm text-ink2">Create, edit, or delete the category tree (PRD §5.8.3). Deleting a category with businesses requires moving them first.</p>
        </div>
        <Button onClick={() => setCreating({ parentId: null })}>
          <Plus className="h-4 w-4" /> Top-level
        </Button>
      </div>

      <Card className="p-2">
        {isLoading ? <PageSpinner /> : (data?.categories ?? []).map((c) => renderNode(c, 0))}
        {!isLoading && !data?.categories?.length && (
          <p className="py-8 text-center text-sm text-ink3">No categories yet. Create the first one.</p>
        )}
      </Card>

      {form && (
        <CategoryForm
          key={form.id || `new-${form.parent_id ?? 'root'}`}
          initial={form}
          isEdit={!!form.id}
          saving={saveMutation.isPending}
          error={(saveMutation.error as Error | null)?.message}
          onSave={(values) => void saveMutation.mutateAsync(values)}
          onCancel={() => {
            setEditing(null)
            setCreating(null)
            saveMutation.reset()
          }}
        />
      )}

      {deleteMutation.error && (
        <p className="mt-4 rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-400">
          {(deleteMutation.error as Error).message}
        </p>
      )}
    </div>
  )
}

function CategoryForm({
  initial,
  isEdit,
  saving,
  error,
  onSave,
  onCancel,
}: {
  initial: CategoryDTO
  isEdit: boolean
  saving: boolean
  error?: string
  onSave: (v: { name: string; slug?: string; parent_id?: string | null; icon?: string; description?: string; sort_order?: number }) => void
  onCancel: () => void
}) {
  const [name, setName] = useState(initial.name)
  const [slug, setSlug] = useState('')
  const [icon, setIcon] = useState(initial.icon ?? '')
  const [description, setDescription] = useState(initial.description ?? '')

  return (
    <Card className="mt-4">
      <p className="mono-label mb-3">{isEdit ? 'Edit category' : 'New category'}</p>
      <form
        className="flex flex-col gap-3 sm:flex-row sm:flex-wrap"
        onSubmit={(e) => {
          e.preventDefault()
          onSave({ name, slug: slug || undefined, parent_id: initial.parent_id, icon: icon || undefined, description: description || undefined })
        }}
      >
        <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} required />
        <Input label="Slug (optional)" value={slug} onChange={(e) => setSlug(e.target.value)} hint="Auto-generated from name when empty" />
        <Input label="Icon key" value={icon} onChange={(e) => setIcon(e.target.value)} hint="coffee, camera, scissors, dumbbell, wrench, shirt…" />
        <Input label="Description" value={description} onChange={(e) => setDescription(e.target.value)} />
        <div className="flex items-end gap-2">
          <Button type="submit" disabled={saving}>{isEdit ? 'Save' : 'Create'}</Button>
          <Button variant="secondary" onClick={onCancel}>Cancel</Button>
        </div>
      </form>
      {error && <p className="mt-3 text-sm text-red-600 dark:text-red-400">{error}</p>}
    </Card>
  )
}
