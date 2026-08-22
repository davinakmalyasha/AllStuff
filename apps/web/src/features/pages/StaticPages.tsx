import { Card } from '@/components/ui/Card'

export function CategoriesPage() {
  return (
    <div className="container-page py-10">
      <p className="mono-label mb-2">Directory</p>
      <h1 className="text-2xl font-semibold tracking-tight">Categories</h1>
      <p className="mt-1 text-sm text-ink2">The full category tree lands with M1 (admin-managed, hierarchical).</p>
      <Card className="mt-8 flex flex-col items-center justify-center gap-2 py-16 text-center">
        <p className="font-mono text-sm text-ink3">M1 · categories</p>
        <p className="text-sm text-ink2">Every category that exists. One tree, admin-managed.</p>
      </Card>
    </div>
  )
}

export function ForBusinessPage() {
  return (
    <div className="container-page py-10">
      <p className="mono-label mb-2">For business</p>
      <h1 className="text-2xl font-semibold tracking-tight">For business</h1>
      <p className="mt-1 text-sm text-ink2">
        The registration wizard, storefront builder, and owner dashboard land in M1–M2.
      </p>
      <Card className="mt-8 flex flex-col items-center justify-center gap-2 py-16 text-center">
        <p className="font-mono text-sm text-ink3">M1–M2 · storefront</p>
        <p className="text-sm text-ink2">Free storefronts with products, branding, chat, and verification.</p>
      </Card>
    </div>
  )
}
