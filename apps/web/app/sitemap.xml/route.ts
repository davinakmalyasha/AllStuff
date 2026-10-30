// Go remains the source of truth for the URL inventory (up to 100k /b URLs).
// This route streams the upstream sitemap verbatim instead of duplicating
// slug queries in a second place.

const API = process.env.API_INTERNAL_URL ?? process.env.API_ORIGIN ?? 'http://localhost:8080'

export const revalidate = 3600

export async function GET() {
  try {
    const res = await fetch(`${API}/api/v1/sitemap.xml`, { next: { revalidate: 3600 } })
    if (!res.ok) throw new Error(`upstream ${res.status}`)
    const xml = await res.text()
    return new Response(xml, { headers: { 'Content-Type': 'application/xml; charset=utf-8' } })
  } catch {
    const fallback =
      '<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"></urlset>'
    return new Response(fallback, { headers: { 'Content-Type': 'application/xml; charset=utf-8' } })
  }
}
