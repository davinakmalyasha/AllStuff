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

/** Owner-declared payment methods. Display only: AllStuff never processes the
 *  payment, so this is a fact about the shop, not a checkout. Order matches the
 *  backend's canonical order. */
export const PAYMENT_METHODS: { key: string; label: string }[] = [
  { key: 'cash', label: 'Cash' },
  { key: 'qris', label: 'QRIS' },
  { key: 'transfer', label: 'Bank transfer' },
  { key: 'ewallet', label: 'E-wallet' },
]

/** Labels for the methods stored on a business, ignoring unknown tokens. */
export function paymentMethodLabels(raw: string | undefined | null): string[] {
  if (!raw) return []
  const set = new Set(raw.split(',').map((v) => v.trim()))
  return PAYMENT_METHODS.filter((m) => set.has(m.key)).map((m) => m.label)
}
