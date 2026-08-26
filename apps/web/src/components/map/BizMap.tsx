import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import * as maplibregl from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { api, searchPath, type BusinessDTO, type CategoryDTO, type TrendEntryDTO } from '@/lib/api'
import { escapeHtml } from '@/lib/url'
import { Badge } from '@/components/ui/Badge'

const TILES_LIGHT = 'https://tiles.openstreetmap.org/{z}/{x}/{y}.png'
const TILES_DARK = 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png'

// Muted category tints (PRD §5.2 "category-colored pins"): stable per category id.
const CAT_TINTS = ['#d97706', '#059669', '#2563eb', '#7c3aed', '#dc2626', '#0d9488', '#ea580c', '#db2777', '#4f46e5', '#65a30d']
function catTint(id: string): string {
  let h = 0
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0
  return CAT_TINTS[h % CAT_TINTS.length]
}

// Marker palette follows the app theme (class on <html>), not just the OS
// preference — black pins were unreadable on the dark basemap.
const MARKERS = {
  light: { fill: '#18181b', ring: '#ffffff', text: '#ffffff' },
  dark: { fill: '#e4e4e7', ring: '#18181b', text: '#18181b' },
} as const

function useIsDarkTheme(): boolean {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))
  useEffect(() => {
    const el = document.documentElement
    const obs = new MutationObserver(() => setDark(el.classList.contains('dark')))
    obs.observe(el, { attributes: true, attributeFilter: ['class'] })
    return () => obs.disconnect()
  }, [])
  return dark
}

function applyMarkerTheme(map: maplibregl.Map, dark: boolean) {
  const c = MARKERS[dark ? 'dark' : 'light']
  const setPaint = (layer: string, prop: string, value: unknown) => {
    if (map.getLayer(layer)) {
      // Paint property names span every layer type; the callers above keep
      // layer/prop pairs valid.
      (map.setPaintProperty as (l: string, p: string, v: unknown) => void)(layer, prop, value)
    }
  }
  const setLayout = (layer: string) => {
    if (map.getLayer(layer)) map.setLayoutProperty(layer, 'visibility', 'visible')
  }
  // Basemap tiles: exactly one raster layer visible at a time.
  if (map.getLayer('osm-light')) map.setLayoutProperty('osm-light', 'visibility', dark ? 'none' : 'visible')
  if (map.getLayer('osm-dark')) map.setLayoutProperty('osm-dark', 'visibility', dark ? 'visible' : 'none')
  setLayout('osm-fallback')
  setPaint('clusters', 'circle-color', c.fill)
  setPaint('cluster-count', 'text-color', c.text)
  setPaint('businesses', 'circle-color', c.fill)
  setPaint('businesses-halo', 'circle-stroke-color', c.ring)
  setPaint('businesses-pulse', 'circle-color', c.fill)
}

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
  embedded?: boolean // no search bar / chips / controls (mini maps)
}

/**
 * The universal map (PRD §5.2): viewport search, clustering, category-tinted
 * pins, Booming pulse, Rising halo, hover tooltips, near-me. Reused by the
 * full map page, the homepage widget, and business mini-maps.
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
  const dark = useIsDarkTheme()
  const trendRef = useRef<Record<string, TrendEntryDTO>>({})
  const userPosRef = useRef<{ lat: number; lng: number } | null>(null)
  const popupRef = useRef<maplibregl.Popup | null>(null)
  // Map event handlers are registered once; they read the LATEST markers
  // through this ref. Reading the state variable directly closed over the
  // first-render empty array — pin clicks silently did nothing after load.
  const markersRef = useRef<MapMarker[]>([])
  useEffect(() => {
    markersRef.current = markers
  }, [markers])

  // Init map once. Two raster basemap layers are registered up front and
  // toggled by visibility — avoids source-swap API differences.
  useEffect(() => {
    if (!containerRef.current || mapRef.current) return
    const m = new maplibregl.Map({
      container: containerRef.current,
      style: {
        version: 8,
        sources: {
          'osm-light': { type: 'raster', tiles: [TILES_LIGHT], tileSize: 256, attribution: '© OpenStreetMap' },
          'osm-dark': { type: 'raster', tiles: [TILES_DARK], tileSize: 256, attribution: '© CARTO © OpenStreetMap' },
        },
        layers: [
          { id: 'osm-light', type: 'raster', source: 'osm-light' },
          { id: 'osm-dark', type: 'raster', source: 'osm-dark', layout: { visibility: 'none' } },
        ],
      },
      center,
      zoom,
      attributionControl: { compact: true },
    })
    if (!embedded) {
      m.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right')
      if (navigator.geolocation) {
        m.addControl(new maplibregl.GeolocateControl({
          positionOptions: { enableHighAccuracy: true },
          trackUserLocation: true,
        }), 'top-right')
      }
    }
    mapRef.current = m
    setMap(m)
    return () => {
      m.remove()
      mapRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Markers + basemap follow the theme toggle instantly.
  useEffect(() => {
    if (!map) return
    applyMarkerTheme(map, dark)
  }, [map, dark])

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

  // Viewport query. A monotonic token guards against out-of-order responses:
  // rapid pan/zoom fires overlapping searches, and a slower EARLIER response
  // used to resolve last and revert pins to a viewport the user already left.
  const querySeq = useRef(0)
  const query = async () => {
    if (!mapRef.current) return
    const bounds = mapRef.current.getBounds()
    const seq = ++querySeq.current
    setLoading(true)
    try {
      const pos = userPosRef.current
      const p = {
        bbox: `${bounds.getWest()},${bounds.getSouth()},${bounds.getEast()},${bounds.getNorth()}`,
        category: cats.length ? cats : undefined,
        q: q || undefined,
        verified_only: verifiedOnly || undefined,
        limit: 100,
        ...(pos ? { lat: pos.lat, lng: pos.lng } : {}),
      }
      const res = await api<{ businesses: BusinessDTO[] }>(searchPath(p))
      if (seq !== querySeq.current) return // stale viewport — drop it
      const withTrend = res.businesses.map((b) => {
        const t = trendRef.current[b.id]
        return t ? { ...b, trend: { is_booming: t.is_booming, is_rising: t.is_rising, velocity: t.velocity } } : b
      })
      setMarkers(withTrend)
    } catch {
      // Silent like the rest of the map: transient viewport fetch failures
      // (offline pan, abort during fast zoom) just keep the previous pins.
    } finally {
      if (seq === querySeq.current) setLoading(false)
    }
  }

  // Single stable handler per registration: re-running this effect removes
  // the exact listener it added (the old closure-based off() leaked).
  useEffect(() => {
    if (!map) return
    let t: ReturnType<typeof setTimeout> | undefined
    // Debounced on every trigger (init + moveend): each pan previously fired
    // a full two-phase search immediately; 300ms settles the gesture first.
    const runLater = () => {
      if (t) clearTimeout(t)
      t = setTimeout(() => void query(), 300)
    }
    runLater()
    map.on('moveend', runLater)
    return () => {
      if (t) clearTimeout(t)
      map.off('moveend', runLater)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [map, cats, q, verifiedOnly])

  // Render GeoJSON layer with clustering + category filter + trend styling.
  useEffect(() => {
    if (!map) return
    const features = markers.map((b) => ({
      type: 'Feature' as const,
      geometry: { type: 'Point' as const, coordinates: [b.lng, b.lat] },
      properties: {
        id: b.id, name: b.name, slug: b.slug, city: b.city,
        cat_name: b.category_name ?? '',
        rating: b.rating_avg ?? 0,
        open: !!b.is_open_now,
        booming: b.trend?.is_booming ?? false,
        rising: b.trend?.is_rising ?? false,
        tint: catTint(b.category_id),
        d: b.distance_km ?? -1,
      },
    }))
    const setData = () => {
      const src = map.getSource('biz') as maplibregl.GeoJSONSource | undefined
      if (!src) return false
      src.setData({ type: 'FeatureCollection', features })
      return true
    }
    if (setData()) return

    map.addSource('biz', {
      type: 'geojson',
      data: { type: 'FeatureCollection', features },
      cluster: true,
      clusterMaxZoom: 13,
      clusterRadius: 42,
    })
    map.addLayer({ id: 'clusters', type: 'circle', source: 'biz', filter: ['has', 'point_count'], paint: { 'circle-color': MARKERS.light.fill, 'circle-radius': ['step', ['get', 'point_count'], 16, 20, 20, 60, 26], 'circle-opacity': 0.85 } })
    map.addLayer({ id: 'cluster-count', type: 'symbol', source: 'biz', filter: ['has', 'point_count'], layout: { 'text-field': ['get', 'point_count'], 'text-size': 11 }, paint: { 'text-color': '#ffffff' } as maplibregl.SymbolLayerSpecification['paint'] })

    // Booming pulse: soft expanding glow under the pin (animated below).
    map.addLayer({
      id: 'businesses-pulse', type: 'circle', source: 'biz',
      filter: ['all', ['!', ['has', 'point_count']], ['get', 'booming']],
      paint: { 'circle-radius': 10, 'circle-color': MARKERS.light.fill, 'circle-opacity': 0.3 },
    })
    // Rising halo: soft outer ring marks hidden gems (dashes unsupported in
    // circle paint, so a low-opacity solid ring reads as the "↑" badge).
    map.addLayer({
      id: 'businesses-halo', type: 'circle', source: 'biz',
      filter: ['all', ['!', ['has', 'point_count']], ['get', 'rising'], ['!', ['get', 'booming']]],
      paint: {
        'circle-radius': 13, 'circle-opacity': 0,
        'circle-stroke-color': MARKERS.light.ring, 'circle-stroke-width': 1.5,
      },
    })
    // Base pins: monochrome fill, category-tinted ring.
    map.addLayer({
      id: 'businesses', type: 'circle', source: 'biz', filter: ['!', ['has', 'point_count']],
      paint: {
        'circle-radius': ['case', ['get', 'booming'], 10, 8],
        'circle-color': MARKERS.light.fill,
        'circle-stroke-color': ['get', 'tint'],
        'circle-stroke-width': 3,
      },
    })

    const featureAt = (e: maplibregl.MapLayerMouseEvent) => {
      const f = e.features?.[0]
      if (!f) return undefined
      return markersRef.current.find((m) => m.id === f.properties?.id)
    }
    map.on('click', 'businesses', (e: maplibregl.MapLayerMouseEvent) => {
      const b = featureAt(e)
      if (!b) return
      setSelected(b)
      map.easeTo({ center: [b.lng, b.lat] })
      onSelect?.(b)
    })
    map.on('click', 'clusters', (e: maplibregl.MapLayerMouseEvent) => {
      const f = e.features?.[0]
      if (!f) return
      const coords = (f.geometry as unknown as { coordinates: [number, number] }).coordinates
      map.easeTo({ center: coords, zoom: (map.getZoom() ?? 0) + 2 })
    })
    map.on('mouseenter', 'businesses', (e: maplibregl.MapLayerMouseEvent) => {
      map.getCanvas().style.cursor = 'pointer'
      const f = e.features?.[0]
      if (!f) return
      const p = f.properties as Record<string, string>
      const dist = Number(p.d)
      if (!popupRef.current) popupRef.current = new maplibregl.Popup({ closeButton: false, offset: 12 })
      popupRef.current
        .setLngLat((f.geometry as unknown as { coordinates: [number, number] }).coordinates)
        // setHTML injects raw markup — owner-controlled business names are
        // escaped here (a <img onerror> name previously executed on hover).
        .setHTML(
          `<div style="font:500 12px/1.4 system-ui,sans-serif;color:#18181b">${escapeHtml(p.name ?? '')}` +
          `<br/><span style="color:#71717a;font-weight:400">${escapeHtml(p.cat_name ?? '')} · ${escapeHtml(p.city ?? '')}` +
          (dist > 0 ? ` · ${dist.toFixed(1)} km` : '') + `</span></div>`,
        )
        .addTo(map)
    })
    map.on('mouseleave', 'businesses', () => {
      map.getCanvas().style.cursor = ''
      popupRef.current?.remove()
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [map, markers])

  // Animate the Booming pulse (radius/opacity breathing cycle) — only while
  // at least one booming pin exists; a 20 fps GL repaint loop with zero
  // booming pins was pure battery burn on the common case.
  const hasBooming = markers.some((m) => m.trend?.is_booming)
  useEffect(() => {
    if (!map || !hasBooming) return
    let phase = 0
    const iv = window.setInterval(() => {
      if (!map.getLayer('businesses-pulse')) return
      phase += 0.08
      const wave = (Math.sin(phase) + 1) / 2
      map.setPaintProperty('businesses-pulse', 'circle-radius', 9 + 9 * wave)
      map.setPaintProperty('businesses-pulse', 'circle-opacity', 0.35 * (1 - wave))
    }, 50)
    return () => clearInterval(iv)
  }, [map, hasBooming])

  // Category chips (non-embedded only).
  const { data: catData } = useQuery({ queryKey: ['categories'], queryFn: () => api<{ categories: CategoryDTO[] }>('/categories') })

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
        <div className="absolute bottom-4 left-1/2 z-20 w-full max-w-[calc(100%-2rem)] -translate-x-1/2 rounded-xl border border-border bg-surface p-4 shadow-cardHover sm:left-auto sm:right-4 sm:w-72 sm:max-w-none sm:translate-x-0">
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
            {!selected.trend?.is_booming && selected.trend?.is_rising && <Badge tone="attention">↑ Rising</Badge>}
          </div>
          <div className="mt-2 flex items-center justify-between">
            <span className="text-xs text-ink3">
              {selected.review_count > 0 ? `★ ${selected.rating_avg?.toFixed(1)} (${selected.review_count})` : 'No reviews yet'}
            </span>
            <Link to={`/b/${selected.slug}`} className="rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-accent-ink">
              View business
            </Link>
          </div>
        </div>
      )}
    </div>
  )
}
