import { Link } from 'react-router-dom'
import { MapPin } from 'lucide-react'
import { BizMap } from '@/components/map/BizMap'

/** Bounded homepage widget (PRD §5.1.1) linking to the full map. */
export function HomeMapWidget() {
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
        <div className="h-80 overflow-hidden rounded-xl border border-border shadow-card">
          <BizMap embedded className="h-full w-full" />
        </div>
      </div>
    </section>
  )
}
