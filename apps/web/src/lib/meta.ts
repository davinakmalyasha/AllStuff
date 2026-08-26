import { useEffect } from 'react'
import i18n from '@/lib/i18n'

const SITE_NAME = 'BizVerse'
const DEFAULT_TITLE = 'BizVerse — Every business. One place.'
const DEFAULT_DESCRIPTION = 'Every business in the world, in one place. Find, compare, and contact any business.'

function ogLocale(lang: string): string {
  return lang?.startsWith('id') ? 'id_ID' : 'en_US'
}

/** Client-side SEO helper (PRD §9.4): sets document title + meta description
 *  + OG tags (incl. the dynamic OG image for businesses, B4). */
export function usePageMeta(title: string, description?: string, og?: { image?: string; url?: string }) {
  const lang = i18n.language ?? 'en'
  useEffect(() => {
    document.title = title ? `${title} · ${SITE_NAME}` : DEFAULT_TITLE
    const setMeta = (attr: 'name' | 'property', key: string, content: string) => {
      let meta = document.querySelector<HTMLMetaElement>(`meta[${attr}="${key}"]`)
      if (!meta) {
        meta = document.createElement('meta')
        meta.setAttribute(attr, key)
        document.head.appendChild(meta)
      }
      meta.content = content
    }
    const desc = description ?? DEFAULT_DESCRIPTION
    setMeta('name', 'description', desc)
    setMeta('property', 'og:title', title || DEFAULT_TITLE)
    setMeta('property', 'og:description', desc)
    setMeta('property', 'og:type', 'website')
    setMeta('property', 'og:url', og?.url ?? window.location.href)
    setMeta('property', 'og:site_name', SITE_NAME)
    const locale = ogLocale(lang)
    setMeta('property', 'og:locale', locale)
    setMeta('property', 'og:locale:alternate', locale === 'id_ID' ? 'en_US' : 'id_ID')
    const image = og?.image ?? `${window.location.origin}/icon-512.svg`
    setMeta('property', 'og:image', image)
    if (og?.image) {
      setMeta('property', 'og:image:width', '1200')
      setMeta('property', 'og:image:height', '630')
    } else {
      document.querySelectorAll('meta[property="og:image:width"], meta[property="og:image:height"]').forEach((m) => m.remove())
    }
    setMeta('name', 'twitter:card', 'summary_large_image')
    setMeta('name', 'twitter:title', title || DEFAULT_TITLE)
    setMeta('name', 'twitter:description', desc)
    setMeta('name', 'twitter:image', image)
    // Canonical link (Batch 3).
    let canonical = document.querySelector<HTMLLinkElement>('link[rel="canonical"]')
    if (!canonical) {
      canonical = document.createElement('link')
      canonical.rel = 'canonical'
      document.head.appendChild(canonical)
    }
    canonical.href = og?.url ?? window.location.href.split('?')[0]
  }, [title, description, og?.image, og?.url, lang])
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
    // Escape "<" so user-controlled fields (business names/descriptions)
    // cannot close the script tag. textContent is safe in the live DOM, but
    // the prerender step serializes page.content() into static HTML where a
    // raw "</script>" inside the JSON would break out into stored XSS.
    script.textContent = JSON.stringify(data).replace(/</g, '\\u003c')
    document.head.appendChild(script)
    return () => {
      document.getElementById(id)?.remove()
    }
  }, [data])
}
