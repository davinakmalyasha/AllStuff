import { BizMap } from '@/components/map/BizMap'
import { usePageMeta } from '@/lib/meta'

export function MapPage() {
  usePageMeta('Map — discover businesses around you')
  return (
    <div className="relative h-[calc(100vh-64px)]">
      <BizMap className="h-full w-full" />
    </div>
  )
}
