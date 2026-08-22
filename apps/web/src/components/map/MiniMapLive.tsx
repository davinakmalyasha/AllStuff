import { lazy, Suspense } from 'react'

// Reuses the lazy-loaded maplibre chunk — no extra entry cost.
const BizMap = lazy(() => import('@/components/map/BizMap').then((m) => ({ default: m.BizMap })))

/** Interactive business mini-map (PRD §5.2): replaces the static tile image,
 *  which depended on a discontinued external service. */
export function MiniMapLive({ lat, lng, name }: { lat: number; lng: number; name: string }) {
  return (
    <div className="relative h-full w-full" title={name}>
      <Suspense fallback={<div className="h-full w-full animate-pulse bg-surface2" />}>
        <BizMap embedded showControls={false} center={[lng, lat]} zoom={15} className="h-full w-full" />
      </Suspense>
    </div>
  )
}
