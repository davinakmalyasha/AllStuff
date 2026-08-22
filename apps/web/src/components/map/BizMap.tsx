import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import * as maplibregl from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { api, searchPath, type BusinessDTO, type CategoryDTO, type TrendEntryDTO } from '@/lib/api'
import { Badge } from '@/components/ui/Badge'

const TILES = 'https://tiles.openstreetmap.org/{z}/{x}/{y}.png' // dark variant below (Batch 3)
const DARK_TILES = 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png'

export interface MapMarker extends BusinessDTO {
  trend?: { is_booming: boolean; is_rising: boolean; velocity: number }
}

interface BizMapProps {
  className?: string
  center?: [number, number]
  zoom?: number
  showControls?: boolean
  initialCategories?: string[]
  onSelect?: (b: MapMarker) => void
  embedded?: boolean // no search bar / chips (mini maps)
}

/**
 * The universal map (PRD §5.2): viewport search, clustering, category layers,
 * Booming pulse markers, Rising rings, near-me. Reused by the full map page,
 * the homepage widget, and business mini-maps.
 */
export function BizMap({ className = '', center = [106.82, -6.2], zoom = 11, showControls = true, initialCategories = [], onSelect, embedded = false }: BizMapProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const mapRef = useRef<maplibregl.Map | null>(null)
  const [map, setMap] = useState<maplibregl.Map | null>(null)
  const [markers, setMarkers] = useState<MapMarker[]>([])
  const [cats, setCats] = useState<string[]>(initialCategories)
  const [q, setQ] = useState('')
  const [verifiedOnly, setVerifiedOnly] = useState(false)
  const [selected, setSelected] = useState<MapMarker | null>(null)
  const [loading, setLoading] = useState(false)
  const trendRef = useRef<Record<string, TrendEntryDTO>>({})

  // Init map once.
  useEffect(() => {
    if (!containerRef.current || mapRef.current) return
    const m = new maplibregl.Map({
      container: containerRef.current,
      style: {
        version: 8,
        sources: {
          osm: { type: 'raster', tiles: [window.matchMedia?.('(prefers-color-scheme: dark)').matches ? DARK_TILES : TILES], tileSize: 256, attribution: '© OpenStreetMap' },
        },
        layers: [{ id: 'osm', type: 'raster', source: 'osm' }],
      },
      center,
      zoom,
    })
    m.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right')
    if (!embedded && navigator.geolocation) {
      m.addControl(new maplibregl.GeolocateControl({
        positionOptions: { enableHighAccuracy: true },
        trackUserLocation: true,
      }), 'top-right')
    }
    mapRef.current = m
    setMap(m)
    return () => {
      m.remove()
      mapRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Fetch trending/rising for badge styling.
  useEffect(() => {
    const load = async () => {
      try {
        const [t, r] = await Promise.all([
          api<{ entries: TrendEntryDTO[] }>('/trending').catch(() => ({ entries: [] })),
          api<{ entries: TrendEntryDTO[] }>('/rising').catch(() => ({ entries: [] })),
        ])
        const merged: Record<string, TrendEntryDTO> = {}
        for (const e of t.entries) merged[e.id] = e
        for (const e of r.entries) {
          if (!merged[e.id]) merged[e.id] = e
          else merged[e.id] = { ...merged[e.id], is_rising: e.is_rising }
        }
        trendRef.current = merged
      } catch {
        /* noop */
      }
    }
    void load()
  }, [])

  // Viewport query.
  const query = async () => {
    if (!mapRef.current) return
    const bounds = mapRef.current.getBounds()
    setLoading(true)
    try {
      const p = {
        bbox: `${bounds.getWest()},${bounds.getSouth()},${bounds.getEast()},${bounds.getNorth()}`,
        category: cats.length ? cats : undefined,
        q: q || undefined,
        verified_only: verifiedOnly || undefined,
        limit: 100,
      }
      const res = await api<{ businesses: BusinessDTO[] }>(searchPath(p))
      const withTrend = res.businesses.map((b) => {
        const t = trendRef.current[b.id]
        return t ? { ...b, trend: { is_booming: t.is_booming, is_rising: t.is_rising, velocity: t.velocity } } : b
      })
      setMarkers(withTrend)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!map) return
    const debounced = setTimeout(() => void query(), 400)
    map.on('moveend', () => void query())
    return () => {
      clearTimeout(debounced)
      map.off('moveend', () => void query())
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [map, cats, q, verifiedOnly])

  // Render GeoJSON layer with clustering + category filter.
  useEffect(() => {
    if (!map) return
    const sourceId = 'biz'
    const src = map.getSource(sourceId)
    if (src) {
      ;(src as maplibregl.GeoJSONSource).setData({
        type: 'FeatureCollection',
        features: markers.map((b) => ({
          type: 'Feature' as const,
          geometry: { type: 'Point' as const, coordinates: [b.lng, b.lat] },
          properties: {
            id: b.id, name: b.name, slug: b.slug, city: b.city,
            category: b.category_id, rating: b.rating_avg ?? 0,
            review_count: b.review_count, price: b.price_level ?? 0,
            open: !!b.is_open_now,
            booming: b.trend?.is_booming ?? false,
            rising: b.trend?.is_rising ?? false,
            logo: b.logo_url,
          },
        })),
      })
      return
    }
    map.addSource(sourceId, {
      type: 'geojson',
      data: { type: 'FeatureCollection', features: [] },
      cluster: true,
      clusterMaxZoom: 13,
      clusterRadius: 42,
    })
    map.addLayer({ id: 'clusters', type: 'circle', source: sourceId, filter: ['has', 'point_count'], paint: { 'circle-color': '#18181b', 'circle-radius': ['step', ['get', 'point_count'], 16, 20, 20, 60, 26], 'circle-opacity': 0.85 } })
    map.addLayer({ id: 'cluster-count', type: 'symbol', source: sourceId, filter: ['has', 'point_count'], layout: { 'text-field': ['get', 'point_count'], 'text-size': 11 }, paint: { 'text-color': '#ffffff' } as maplibregl.SymbolLayerSpecification['paint'] })
    map.addLayer({
      id: 'businesses', type: 'circle', source: sourceId, filter: ['!', ['has', 'point_count']],
      paint: {
        'circle-radius': ['case', ['get', 'booming'], 13, ['get', 'rising'], 11, 8],
        'circle-color': ['case', ['get', 'booming'], '#000000', ['get', 'rising'], '#555555', '#18181b'],
        'circle-stroke-color': '#ffffff',
        'circle-stroke-width': ['case', ['get', 'booming'], 3, ['get', 'rising'], 2, 1.5],
      },
    })
    map.on('click', 'businesses', (e: maplibregl.MapLayerMouseEvent) => {
      const f = e.features?.[0]
      if (!f) return
      const b = markers.find((m) => m.id === f.properties?.id)
      if (b) {
        setSelected(b)
        map.easeTo({ center: [b.lng, b.lat] })
        onSelect?.(b)
      }
    })
    map.on('click', 'clusters', (e: maplibregl.MapLayerMouseEvent) => {
      const f = e.features?.[0]
      if (!f) return
      const coords = (f.geometry as unknown as { coordinates: [number, number] }).coordinates
      map.easeTo({ center: coords, zoom: (map.getZoom() ?? 0) + 2 })
    })
    map.on('mouseenter', 'businesses', () => { map.getCanvas().style.cursor = 'pointer' })
    map.on('mouseleave', 'businesses', () => { map.getCanvas().style.cursor = '' })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [map, markers])

  // Category chips (non-embedded only).
  const { data: catData } = useCategories()

  return (
    <div className={`relative ${className}`}>
      {!embedded && showControls && (
        <div className="absolute left-3 right-3 top-3 z-20 space-y-2">
          <div className="flex items-center gap-2 rounded-xl border border-border bg-surface p-2 shadow-card">
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && void query()}
              placeholder="Search on the map…"
              className="h-9 w-full bg-transparent text-sm text-ink placeholder:text-ink3 focus:outline-none"
            />
            {loading && <span className="text-xs text-ink3">…</span>}
            <label className="flex shrink-0 items-center gap-1.5 text-xs text-ink2">
              <input type="checkbox" checked={verifiedOnly} onChange={(e) => setVerifiedOnly(e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" />
              Verified
            </label>
          </div>
          <div className="flex flex-wrap gap-1.5">
            {(catData?.categories ?? []).filter((c) => c.parent_id).slice(0, 12).map((c) => (
              <button
                key={c.id}
                onClick={() => setCats((prev) => (prev.includes(c.id) ? prev.filter((x) => x !== c.id) : [...prev, c.id]))}
                className={`rounded-full border px-2.5 py-1 text-xs transition-colors ${
                  cats.includes(c.id) ? 'border-ink bg-accent text-accent-ink' : 'border-border bg-surface text-ink2 hover:bg-surface2'
                }`}
              >
                {c.name}
              </button>
            ))}
          </div>
        </div>
      )}

      <div ref={containerRef} className="h-full w-full" />

      {selected && (
        <div className="absolute bottom-4 left-1/2 z-20 w-72 -translate-x-1/2 rounded-xl border border-border bg-surface p-4 shadow-cardHover sm:left-auto sm:right-4 sm:translate-x-0">
          <div className="flex items-center gap-3">
            {selected.logo_url ? (
              <img src={selected.logo_url} alt="" className="h-10 w-10 rounded-lg object-cover" />
            ) : (
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-surface2 text-sm font-semibold">{selected.name.charAt(0)}</div>
            )}
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-semibold text-ink">{selected.name}</p>
              <p className="text-xs text-ink3">{selected.city} · {selected.category_name}</p>
            </div>
            {selected.trend?.is_booming && <Badge tone="attention" dot>Booming</Badge>}
          </div>
          <div className="mt-2 flex items-center justify-between">
            <span className="text-xs text-ink3">
              {selected.review_count > 0 ? `★ ${selected.rating_avg?.toFixed(1)} (${selected.review_count})` : 'No reviews yet'}
            </span>
            <a href={`/b/${selected.slug}`} className="rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-accent-ink">
              View business
            </a>
          </div>
        </div>
      )}
    </div>
  )
}

function useQueryCats() {
  return useQuery({ queryKey: ['categories'], queryFn: () => api<{ categories: CategoryDTO[] }>('/categories') })
}

function useCategories() {
  const { data } = useQueryCats()
  return { data }
}
