import { useEffect } from 'react'

/** Client-side SEO helper (PRD §9.4): sets document title + meta description
 *  + OG tags (incl. the dynamic OG image for businesses, B4). */
export function usePageMeta(title: string, description?: string, og?: { image?: string; url?: string }) {
  useEffect(() => {
    document.title = title ? `${title} · BizVerse` : 'BizVerse — Every business. One place.'
    const setMeta = (attr: 'name' | 'property', key: string, content: string) => {
      let meta = document.querySelector<HTMLMetaElement>(`meta[${attr}="${key}"]`)
      if (!meta) {
        meta = document.createElement('meta')
        meta.setAttribute(attr, key)
        document.head.appendChild(meta)
      }
      meta.content = content
    }
    const desc = description ?? 'Every business in the world, in one place. Find, compare, and contact any business.'
    setMeta('name', 'description', desc)
    setMeta('property', 'og:title', title || 'BizVerse')
    setMeta('property', 'og:description', desc)
    setMeta('property', 'og:type', 'website')
    setMeta('property', 'og:url', og?.url ?? window.location.href)
    if (og?.image) setMeta('property', 'og:image', og.image)
    else setMeta('property', 'og:image', `${window.location.origin}/icon-512.svg`)
    setMeta('name', 'twitter:card', 'summary_large_image')
    // Canonical link (Batch 3).
    let canonical = document.querySelector<HTMLLinkElement>('link[rel="canonical"]')
    if (!canonical) {
      canonical = document.createElement('link')
      canonical.rel = 'canonical'
      document.head.appendChild(canonical)
    }
    canonical.href = og?.url ?? window.location.href.split('?')[0]
  }, [title, description, og?.image, og?.url])
}

/** Injects Schema.org JSON-LD (LocalBusiness / CategoryCode / etc.). */
export function useJsonLd(data: Record<string, unknown> | null) {
  useEffect(() => {
    if (!data) return
    const id = 'bv-jsonld'
    document.getElementById(id)?.remove()
    const script = document.createElement('script')
    script.id = id
    script.type = 'application/ld+json'
    script.textContent = JSON.stringify(data)
    document.head.appendChild(script)
    return () => {
      document.getElementById(id)?.remove()
    }
  }, [data])
}
