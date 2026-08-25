// URL safety helpers — the only sanctioned gate for rendering user-supplied
// links. Nothing in the app may pass raw owner-controlled strings to href/src.

const ALLOWED_PROTOCOLS = new Set(['http:', 'https:', 'mailto:', 'tel:'])

/**
 * Returns a safe href for a user-supplied URL, or null when the value must
 * not be linked at all. Blocks javascript:/data:/vbscript: and other active
 * schemes (owner-set website fields and chat link previews previously went
 * straight into href).
 */
export function safeExternalUrl(raw: string | null | undefined): string | null {
  const value = (raw ?? '').trim()
  if (!value) return null
  try {
    const u = new URL(value, window.location.origin)
    if (!ALLOWED_PROTOCOLS.has(u.protocol)) return null
    // Allow relative paths resolved against our origin, plus absolute
    // http(s)/mailto/tel links.
    return u.href
  } catch {
    return null
  }
}

/**
 * Escape a string for interpolation into HTML/SVG markup (map popups,
 * share cards). Prefer setDOMContent/textContent where possible.
 */
export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => {
    switch (c) {
      case '&': return '&amp;'
      case '<': return '&lt;'
      case '>': return '&gt;'
      case '"': return '&quot;'
      default: return '&#39;'
    }
  })
}
