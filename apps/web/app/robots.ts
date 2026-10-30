import type { MetadataRoute } from 'next'

const API = process.env.API_INTERNAL_URL ?? process.env.API_ORIGIN ?? 'http://localhost:8080'

export default async function robots(): Promise<MetadataRoute.Robots> {
  // Go remains the source of truth for crawl rules; fall back to a safe
  // default when the API is unreachable at build time.
  try {
    const res = await fetch(`${API}/robots.txt`, { next: { revalidate: 3600 } })
    if (res.ok) {
      const text = await res.text()
      const disallow = [...text.matchAll(/^Disallow:\s*(\S+)/gm)].map((m) => m[1])
      return { rules: [{ userAgent: '*', allow: '/', disallow: disallow.length ? disallow : ['/me/', '/dashboard/', '/admin/'] }] }
    }
  } catch {
    /* fall through */
  }
  return { rules: [{ userAgent: '*', allow: '/', disallow: ['/me/', '/dashboard/', '/admin/'] }] }
}
