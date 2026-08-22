import { useState } from 'react'
import { Plus, Trash2 } from 'lucide-react'

const AMENITY_PRESETS = ['Wifi', 'Parking', 'Wheelchair accessible', 'Outdoor seating', 'Takeaway', 'Delivery', 'Vegan options', 'Halal', 'Kids friendly', 'Pet friendly', 'Air conditioning', 'Private room']

/** Amenities chips editor (Batch 2). */
export function AmenityEditor({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const [custom, setCustom] = useState('')
  const toggle = (a: string) =>
    onChange(value.includes(a) ? value.filter((x) => x !== a) : value.length >= 12 ? value : [...value, a])

  return (
    <div>
      <p className="mb-2 text-sm font-medium text-ink">Amenities</p>
      <div className="flex flex-wrap gap-2">
        {AMENITY_PRESETS.map((a) => (
          <button
            key={a}
            type="button"
            onClick={() => toggle(a)}
            className={`rounded-full border px-3 py-1 text-xs transition-colors ${
              value.includes(a) ? 'border-ink bg-accent text-accent-ink' : 'border-border text-ink2 hover:bg-surface2'
            }`}
          >
            {value.includes(a) ? '✓ ' : ''}{a}
          </button>
        ))}
        {value.filter((v) => !AMENITY_PRESETS.includes(v)).map((a) => (
          <span key={a} className="flex items-center gap-1 rounded-full border border-ink bg-accent px-3 py-1 text-xs text-accent-ink">
            ✓ {a}
            <button onClick={() => toggle(a)} aria-label={`Remove ${a}`}><Trash2 className="h-3 w-3" /></button>
          </span>
        ))}
      </div>
      <div className="mt-2 flex gap-2">
        <input value={custom} onChange={(e) => setCustom(e.target.value)} placeholder="Custom amenity…" className="h-8 flex-1 rounded-lg border border-border bg-surface px-3 text-sm text-ink" />
        <button type="button" className="rounded-lg border border-border px-3 text-sm text-ink hover:bg-surface2" onClick={() => { if (custom.trim()) { toggle(custom.trim()); setCustom('') } }}>
          <Plus className="inline h-3.5 w-3.5" /> Add
        </button>
      </div>
    </div>
  )
}

export type SpecialHours = Record<string, { open: string; close: string; closed: boolean }>

/** Special/holiday hours editor (Batch 2): per-date overrides. */
export function SpecialHoursEditor({ value, onChange }: { value: SpecialHours; onChange: (v: SpecialHours) => void }) {
  const [date, setDate] = useState('')
  const [open, setOpen] = useState('10:00')
  const [close, setClose] = useState('18:00')
  const [closed, setClosed] = useState(false)

  const add = () => {
    if (!date) return
    onChange({ ...value, [date]: { open, close, closed } })
    setDate('')
  }

  return (
    <div>
      <p className="mb-2 text-sm font-medium text-ink">Special hours (holidays)</p>
      <div className="flex flex-wrap items-end gap-2">
        <input type="date" value={date} onChange={(e) => setDate(e.target.value)} className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink" />
        <label className="flex items-center gap-1 text-xs text-ink2">
          <input type="checkbox" checked={closed} onChange={(e) => setClosed(e.target.checked)} className="h-3.5 w-3.5 accent-black dark:accent-white" /> Closed
        </label>
        {!closed && (
          <>
            <input type="time" value={open} onChange={(e) => setOpen(e.target.value)} className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink" />
            <span className="text-ink3">–</span>
            <input type="time" value={close} onChange={(e) => setClose(e.target.value)} className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink" />
          </>
        )}
        <button type="button" onClick={add} className="rounded-lg border border-border px-3 py-2 text-sm text-ink hover:bg-surface2" disabled={!date}>
          <Plus className="inline h-3.5 w-3.5" /> Add
        </button>
      </div>
      {Object.keys(value).length > 0 && (
        <div className="mt-2 flex flex-wrap gap-2">
          {Object.entries(value).map(([d, v]) => (
            <span key={d} className="flex items-center gap-1.5 rounded-full bg-surface2 px-2.5 py-1 text-xs text-ink2">
              {d} {v.closed ? '· closed' : `· ${v.open}–${v.close}`}
              <button onClick={() => onChange(Object.fromEntries(Object.entries(value).filter(([k]) => k !== d)))} aria-label={`Remove ${d}`}><Trash2 className="h-3 w-3" /></button>
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
