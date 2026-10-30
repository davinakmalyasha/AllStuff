"use client"

import { Input } from '@/components/ui/Input'
import type { DayHoursDTO } from '@/lib/api'

const DAYS: { key: string; label: string }[] = [
  { key: 'mon', label: 'Monday' },
  { key: 'tue', label: 'Tuesday' },
  { key: 'wed', label: 'Wednesday' },
  { key: 'thu', label: 'Thursday' },
  { key: 'fri', label: 'Friday' },
  { key: 'sat', label: 'Saturday' },
  { key: 'sun', label: 'Sunday' },
]

type Mode = 'inherit' | 'working' | 'off'

function modeOf(h: DayHoursDTO | undefined): Mode {
  if (!h) return 'inherit'
  return h.working ? 'working' : 'off'
}

/**
 * A weekday editor shared by staff shifts and per-service hours.
 *
 * The three states are the whole point. "Inherit" is not the same as "closed":
 * a shop that never configured a day still follows its opening hours, while a
 * day explicitly marked off stays off even if the shop is open. Collapsing the
 * two into a checkbox would make a staff member's day off silently become a
 * full shift the next time the business's hours changed.
 *
 * Switching to "off" keeps whatever times were typed, so switching back does not
 * throw the shift away. The API accepts times on a non-working day for exactly
 * that reason.
 */
export function HoursEditor({
  value,
  onChange,
  defaults,
  label,
}: {
  value: Record<string, DayHoursDTO>
  onChange: (next: Record<string, DayHoursDTO>) => void
  /** Times a newly-enabled day starts with, e.g. the location's opening hours. */
  defaults?: { opens_at: string; closes_at: string }
  label: string
}) {
  const set = (day: string, next: DayHoursDTO | undefined) => {
    const copy = { ...value }
    if (next) copy[day] = next
    else delete copy[day]
    onChange(copy)
  }

  return (
    <div className="space-y-2">
      {DAYS.map(({ key, label: dayLabel }) => {
        const h = value[key]
        const mode = modeOf(h)
        const opens = h?.opens_at || defaults?.opens_at || '09:00'
        const closes = h?.closes_at || defaults?.closes_at || '17:00'
        return (
          <div key={key} className="grid grid-cols-[6.5rem_7rem_1fr] items-center gap-2">
            <span className="text-sm text-ink">{dayLabel}</span>
            <select
              aria-label={`${dayLabel} ${label}`}
              value={mode}
              onChange={(e) => {
                const next = e.target.value as Mode
                if (next === 'inherit') set(key, undefined)
                else if (next === 'off') set(key, { working: false, opens_at: opens, closes_at: closes })
                else set(key, { working: true, opens_at: opens, closes_at: closes })
              }}
              className="h-9 rounded-lg border border-border bg-surface px-2 text-sm text-ink"
            >
              <option value="inherit">Inherit</option>
              <option value="working">Working</option>
              <option value="off">Off</option>
            </select>
            {mode === 'working' ? (
              <div className="flex items-center gap-1.5">
                <Input
                  aria-label={`${dayLabel} opens at`}
                  type="time"
                  value={opens}
                  onChange={(e) => set(key, { working: true, opens_at: e.target.value, closes_at: closes })}
                  className="h-9"
                />
                <span className="text-xs text-ink3">to</span>
                <Input
                  aria-label={`${dayLabel} closes at`}
                  type="time"
                  value={closes}
                  onChange={(e) => set(key, { working: true, opens_at: opens, closes_at: e.target.value })}
                  className="h-9"
                />
              </div>
            ) : (
              <p className="text-xs text-ink3">
                {mode === 'inherit' ? `Same as the ${label === 'shift' ? 'business' : 'location'}.` : 'Not available this day.'}
              </p>
            )}
          </div>
        )
      })}
    </div>
  )
}
