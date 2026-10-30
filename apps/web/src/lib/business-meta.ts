// Shared display helpers for business facts that appear on every surface.
//
// These four values (price level, distance, open state, WhatsApp link) were
// re-implemented inline in BusinessCard, BusinessPage, ComparePage and the map
// popup, so a change to any one of them drifted on the others. One module keeps
// the wording and the null-handling consistent without changing what any
// surface renders today.

/** Price level as `$`s, or an em dash when the owner never set one. */
export function priceLevelLabel(level: number | null | undefined): string {
  return '$'.repeat(level ?? 0) || '—'
}

/**
 * Distance in kilometres, one decimal. `undefined` is "no origin supplied"
 * (logged-out search), which is different from "0 km away", so the caller
 * decides whether to render at all.
 */
export function distanceLabel(km: number | null | undefined): string | undefined {
  if (km === null || km === undefined) return undefined
  return `${km.toFixed(1)} km`
}

/**
 * Open state for a business whose hours are known. Undefined means the listing
 * carries no hours data (trend rows omit it), and every surface chooses to say
 * nothing rather than claim "closed".
 */
export function isOpenKnown(open: boolean | null | undefined): open is boolean {
  return typeof open === 'boolean'
}

/** Canonical WhatsApp deep link. Handles may include a leading `+`. */
export function whatsappUrl(handle: string): string {
  return `https://wa.me/${handle.replace(/^\+/, '')}`
}
