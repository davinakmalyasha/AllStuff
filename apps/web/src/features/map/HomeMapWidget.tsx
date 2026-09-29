import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { MapPin } from 'lucide-react'

// maplibre-gl is a 254 kB-gzip chunk plus 10 kB of CSS, and it stays out of the
// entry bundle — which is exactly why gating WHEN it loads matters.
const BizMap = lazy(() => import('@/components/map/BizMap').then((m) => ({ default: m.BizMap })))

/**
 * Bounded homepage widget (PRD §5.1.1) linking to the full map.
 *
 * VIEWPORT GATED. `lazy()` alone does not defer: React mounts the component,
 * the dynamic import fires immediately, and the download competes with LCP. The
 * widget is rendered unconditionally below the fold on `/`, so every homepage
 * visitor — including the majority who never scroll — paid 254 kB gzip of JS
 * plus tile requests.
 *
 * `lazy` only moves the fetch to a microtask after hydration. An
 * IntersectionObserver with a generous rootMargin is what actually defers it
 * until the section is close to the viewport, and it disconnects on first
 * intersection so the cost is paid at most once.
 */
export function HomeMapWidget() {
  const ref = useRef<HTMLDivElement>(null)
  const [near, setNear] = useState(false)

  useEffect(() => {
    const el = ref.current
    if (!el || near) return
    // rootMargin starts the fetch slightly before the section is on screen, so
    // the chunk is usually ready by the time it scrolls into view.
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setNear(true)
          io.disconnect()
        }
      },
      { rootMargin: '300px' },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [near])

  return (
    <section className="border-t border-border py-16 sm:py-20">
      <div className="container-page">
        <div className="mb-6 flex items-center justify-between">
          <div>
            <p className="mono-label mb-2">Explore</p>
            <h2 className="text-xl font-semibold tracking-tight">The map</h2>
          </div>
          <Link to="/map" className="flex items-center gap-1.5 text-sm text-ink underline underline-offset-4 hover:text-ink2">
            <MapPin className="h-4 w-4" /> Open full map
          </Link>
        </div>
        <div
          ref={ref}
          className="h-80 overflow-hidden rounded-xl border border-border shadow-card"
        >
          {near ? (
            <Suspense fallback={<MapPlaceholder />}>
              <BizMap embedded className="h-full w-full" />
            </Suspense>
          ) : (
            <MapPlaceholder />
          )}
        </div>
      </div>
    </section>
  )
}

/**
 * Reserve the exact final height so mounting the map causes no layout shift.
 * The map panel is deliberately a plain block rather than SkeletonCard (which
 * has a different height) because CLS on a below-the-fold section still moves
 * anything beneath it.
 */
function MapPlaceholder() {
  return (
    <div className="flex h-full w-full items-center justify-center bg-surface2" aria-hidden>
      <p className="text-xs text-ink3">Map loads as you scroll</p>
    </div>
  )
}
