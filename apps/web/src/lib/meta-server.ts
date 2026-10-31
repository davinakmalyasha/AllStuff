// Shared server-side SEO helpers. Mirrors the client defaults in
// src/lib/meta.ts (same site name, default title/description, 1200x630 OG
// image) so server metadata and client hydration agree byte-for-byte.

import { siteUrl } from './api-server'
import { whatsappUrl } from './business-meta'

export const SITE_NAME = 'AllStuff'
export const DEFAULT_TITLE = 'AllStuff — Every business. One place.'
export const DEFAULT_DESCRIPTION =
  'Every business in the world, in one place. Find, compare, and contact any business.'

export function absolute(path: string): string {
  const base = siteUrl()
  if (/^https?:\/\//.test(path)) return path
  return `${base}${path.startsWith('/') ? '' : '/'}${path}`
}

/** Escape "<" so user-controlled fields cannot break out of the JSON-LD
 * script tag. Same escaping as the client useJsonLd helper. */
export function jsonLdScript(data: Record<string, unknown>): string {
  return JSON.stringify(data).replace(/</g, '\\u003c')
}

const SCHEMA_DAYS: Record<string, string> = {
  mon: 'Monday',
  tue: 'Tuesday',
  wed: 'Wednesday',
  thu: 'Thursday',
  fri: 'Friday',
  sat: 'Saturday',
  sun: 'Sunday',
}

function cap(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s
}

interface HoursSpec {
  open?: string
  close?: string
  closed?: boolean
}

export function businessJsonLd(b: {
  name: string
  slug: string
  description: string
  tagline?: string | null
  logo_url?: string | null
  cover_url?: string | null
  address: string
  city: string
  country: string
  lat: number
  lng: number
  hours: Record<string, HoursSpec>
  contact: Record<string, string>
  rating_avg?: number | null
  review_count: number
}): Record<string, unknown> {
  const base = siteUrl()
  const SOCIAL_URLS: Record<string, (h: string) => string> = {
    whatsapp: whatsappUrl,
    instagram: (h) => `https://instagram.com/${h}`,
    tiktok: (h) => `https://tiktok.com/@${h}`,
    facebook: (h) => `https://facebook.com/${h}`,
    x: (h) => `https://x.com/${h}`,
    youtube: (h) => `https://youtube.com/@${h}`,
    line: (h) => `https://line.me/R/ti/p/@${h}`,
    telegram: (h) => `https://t.me/${h}`,
  }
  const sameAs = Object.keys(SOCIAL_URLS)
    .filter((k) => b.contact[k])
    .map((k) => SOCIAL_URLS[k]?.(b.contact[k]))
    .filter((u): u is string => !!u)
  return {
    '@context': 'https://schema.org',
    '@type': 'LocalBusiness',
    name: b.name,
    description: b.description,
    image: b.logo_url ?? undefined,
    url: `${base}/b/${b.slug}`,
    telephone: b.contact.phone || undefined,
    address: {
      '@type': 'PostalAddress',
      streetAddress: b.address,
      addressLocality: b.city,
      addressCountry: b.country,
    },
    geo:
      b.lat && b.lng
        ? { '@type': 'GeoCoordinates', latitude: b.lat, longitude: b.lng }
        : undefined,
    openingHoursSpecification: Object.entries(b.hours ?? {})
      .filter(([, h]) => h && !h.closed && h.open && h.close)
      .map(([d, h]) => ({
        '@type': 'OpeningHoursSpecification',
        dayOfWeek: `https://schema.org/${SCHEMA_DAYS[d] ?? cap(d)}`,
        opens: (h as HoursSpec).open,
        closes: (h as HoursSpec).close,
      })),
    sameAs: sameAs.length ? sameAs : undefined,
    aggregateRating:
      b.review_count > 0
        ? { '@type': 'AggregateRating', ratingValue: b.rating_avg, reviewCount: b.review_count }
        : undefined,
  }
}
