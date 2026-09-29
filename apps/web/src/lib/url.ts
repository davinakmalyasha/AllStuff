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
 * The profile link platform set, shared by the owner editor and the public
 * profile. Keys must match `profileLinkKeys` in
 * services/api/internal/service/users.go — the API rejects unknown keys, so
 * this is the form's allowlist, not a free-text map.
 */
export const PROFILE_LINK_FIELDS: { key: string; label: string; placeholder: string }[] = [
  { key: 'website', label: 'Website', placeholder: 'https://example.com' },
  { key: 'email', label: 'Email', placeholder: 'mailto:you@example.com' },
  { key: 'instagram', label: 'Instagram', placeholder: 'https://instagram.com/you' },
  { key: 'x', label: 'X', placeholder: 'https://x.com/you' },
  { key: 'facebook', label: 'Facebook', placeholder: 'https://facebook.com/you' },
  { key: 'linkedin', label: 'LinkedIn', placeholder: 'https://linkedin.com/in/you' },
  { key: 'github', label: 'GitHub', placeholder: 'https://github.com/you' },
  { key: 'youtube', label: 'YouTube', placeholder: 'https://youtube.com/@you' },
  { key: 'tiktok', label: 'TikTok', placeholder: 'https://tiktok.com/@you' },
  { key: 'whatsapp', label: 'WhatsApp', placeholder: 'https://wa.me/15551234567' },
]

/**
 * Turn a profile_links map into renderable {key,label,href} entries, dropping
 * anything the scheme allowlist rejects rather than emitting a broken or
 * dangerous href.
 */
export function profileLinkEntries(links: unknown): { key: string; label: string; href: string }[] {
  const map = (links ?? {}) as Record<string, unknown>
  return PROFILE_LINK_FIELDS.flatMap((f) => {
    const href = safeExternalUrl(typeof map[f.key] === 'string' ? (map[f.key] as string) : null)
    return href ? [{ key: f.key, label: f.label, href }] : []
  })
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

/**
 * Returns a same-origin path for a post-authentication return target
 * (`?next=`), or `fallback` when the value is not a safe internal path.
 *
 * The open-redirect surface here is wider than it looks, because the WHATWG URL
 * parser normalises the value BEFORE react-router's pushState sees it:
 *
 *   //evil.com           protocol-relative → different origin
 *   /\evil.com           backslash is treated as a slash in special schemes
 *   "/\t/evil.com"      U+0009/U+000A/U+000D are STRIPPED from URLs, so the
 *                       control character vanishes and //evil.com remains
 *
 * A character-class check misses the third case: the tab is simply not a "/"
 * or "\", so `/^\/[^/\\]/` matches and the normalised result is cross-origin.
 *
 * Rather than enumerating normalisation tricks, this delegates the decision to
 * the parser: resolve against the current origin and accept only a same-origin
 * result. That is correct for every case the parser knows about, including ones
 * added later.
 */
export function safeInternalPath(raw: string | null | undefined, fallback: string): string {
  const value = (raw ?? '').trim()
  if (!value) return fallback
  // Reject control characters outright. They are stripped rather than rejected
  // by the parser, so they must be refused before parsing, not after.
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(value)) return fallback
  // A scheme, or a protocol-relative / backslash form, is never an internal path.
  if (!value.startsWith('/')) return fallback
  if (value.startsWith('//') || value.includes('\\')) return fallback
  try {
    const base = new URL(window.location.origin)
    const u = new URL(value, base)
    if (u.origin !== base.origin) return fallback
    if (u.protocol !== base.protocol) return fallback
    return `${u.pathname}${u.search}${u.hash}`
  } catch {
    return fallback
  }
}
