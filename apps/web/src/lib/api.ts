// Typed API client — envelope contract (PRD §11.2), cookie auth, CSRF double-submit.

export interface ApiErrorBody {
  error: {
    code: string
    message: string
    fields?: Record<string, string>
  }
}

export class ApiError extends Error {
  code: string
  status: number
  fields?: Record<string, string>

  constructor(code: string, message: string, status: number, fields?: Record<string, string>) {
    super(message)
    this.code = code
    this.status = status
    this.fields = fields
  }
}

const BASE = '/api/v1'

let csrfToken: string | null = null

function readCookie(name: string): string | null {
  const m = document.cookie.match(new RegExp(`(?:^|; )${name}=([^;]*)`))
  return m ? decodeURIComponent(m[1]) : null
}

/** Ensure a CSRF cookie exists (GET /auth/csrf sets it once), then cache it. */
export async function ensureCSRF(): Promise<string> {
  if (csrfToken) return csrfToken
  const existing = readCookie('bv_csrf')
  if (existing) {
    csrfToken = existing
    return existing
  }
  await fetch(`${BASE}/auth/csrf`, { credentials: 'include' })
  const fresh = readCookie('bv_csrf')
  if (!fresh) throw new ApiError('csrf_unavailable', 'CSRF token unavailable.', 0)
  csrfToken = fresh
  return fresh
}

interface RequestOptions extends Omit<RequestInit, 'body'> {
  body?: unknown
}

let refreshing: Promise<boolean> | null = null

/**
 * Try to renew the session once via the refresh cookie. Returns true when a
 * new access cookie was set. Concurrent callers share one refresh call.
 */
function refreshSession(): Promise<boolean> {
  if (!refreshing) {
    refreshing = (async () => {
      try {
        // Mutations need the CSRF double-submit header — without it this
        // request was always 403'd by the middleware before it could even
        // be evaluated, wasting a round trip on every logged-out page load.
        const token = await ensureCSRF().catch(() => '')
        const res = await fetch(`${BASE}/auth/refresh`, {
          method: 'POST',
          credentials: 'include',
          headers: token ? { 'X-CSRF-Token': token } : undefined,
        })
        return res.ok
      } catch {
        return false
      } finally {
        // Release the shared promise slightly later so parallel callers that
        // grabbed it before settle can proceed with the new cookies.
        setTimeout(() => {
          refreshing = null
        }, 0)
      }
    })()
  }
  return refreshing
}

export async function api<T>(path: string, options: RequestOptions = {}, _retried = false): Promise<T> {
  const { body, headers, ...rest } = options

  const method = rest.method ?? 'GET'
  const isMutation = method !== 'GET'
  const isFormData = body instanceof FormData
  const reqHeaders: Record<string, string> = {
    Accept: 'application/json',
    ...(headers as Record<string, string> | undefined),
  }
  if (body !== undefined && !isFormData) reqHeaders['Content-Type'] = 'application/json'
  if (isMutation) {
    try {
      reqHeaders['X-CSRF-Token'] = await ensureCSRF()
    } catch {
      /* cookie may already exist; server will 403 if truly missing */
    }
  }

  const res = await fetch(`${BASE}${path}`, {
    ...rest,
    method,
    credentials: 'include',
    headers: reqHeaders,
    body: body !== undefined ? (isFormData ? body : JSON.stringify(body)) : undefined,
  })

  if (res.status === 204) return undefined as T

  let data: unknown = null
  try {
    data = await res.json()
  } catch {
    /* empty body */
  }

  if (!res.ok) {
    const err = (data as ApiErrorBody | null)?.error
    // Rate limited on idempotent reads: honor Retry-After (cap 60s), retry
    // once. Mutations are NOT auto-retried (non-idempotent).
    if (!_retried && res.status === 429 && !isMutation) {
      const retryAfter = Number(res.headers.get('Retry-After') ?? '5')
      await new Promise((resolve) => setTimeout(resolve, Math.min(retryAfter * 1000, 60_000)))
      return api<T>(path, options, true)
    }
    // Stale CSRF cache (e.g. after logout cleared the cookie): drop it and retry once.
    if (!_retried && res.status === 403 && err?.code === 'csrf_invalid') {
      csrfToken = null
      return api<T>(path, options, true)
    }
    // Access token expired: silently refresh and replay once (never for the
    // auth endpoints themselves, to avoid loops).
    if (
      !_retried &&
      res.status === 401 &&
      !path.startsWith('/auth/') &&
      (await refreshSession())
    ) {
      return api<T>(path, options, true)
    }
    throw new ApiError(
      err?.code ?? 'http_error',
      err?.message ?? `Request failed (${res.status}).`,
      res.status,
      err?.fields,
    )
  }
  return data as T
}

// ---- typed helpers ----

export const get = <T>(path: string) => api<T>(path)
export const post = <T>(path: string, body?: unknown) => api<T>(path, { method: 'POST', body })
export const patch = <T>(path: string, body?: unknown) => api<T>(path, { method: 'PATCH', body })
export const del = <T>(path: string) => api<T>(path, { method: 'DELETE' })

// ---- DTOs ----

export interface UserDTO {
  id: string
  email: string
  name: string
  username: string
  avatar_url: string | null
  bio: string | null
  timezone: string
  profile_links: Record<string, string>
  role: 'user' | 'admin'
  status: 'active' | 'suspended' | 'banned'
  email_verified: boolean
  is_admin: boolean
  created_at: string
}

export interface CategoryDTO {
  id: string
  parent_id: string | null
  name: string
  slug: string
  icon: string
  description: string | null
  sort_order: number
  count: number
  children?: CategoryDTO[]
}

export interface BusinessDTO {
  id: string
  name: string
  slug: string
  tagline: string | null
  description: string
  category_id: string
  status: 'draft' | 'pending_review' | 'verified' | 'rejected' | 'suspended' | 'paused' | 'closed'
  rejection_reason: string | null
  logo_url: string | null
  cover_url: string | null
  gallery: string[]
  price_level: number | null
  currency: string
  address: string
  lat: number
  lng: number
  city: string
  country: string
  timezone: string
  amenities: string[]
  hours: Record<string, { open?: string; close?: string; closed?: boolean }>
  special_hours?: Record<string, { open?: string; close?: string; closed?: boolean }>
  contact: Record<string, string>
  tags: string[]
  founded_year: number | null
  is_featured: boolean
  last_published_at: string | null
  published_snapshot: {
    theme?: { template?: string; colors?: Record<string, string>; font?: string }
    layout?: {
      sections?: { key: string; enabled: boolean }[]
      highlights?: { icon: string; title: string; text: string }[]
    }
  } | null
  theme?: Record<string, unknown>
  layout?: Record<string, unknown>
  verification_level: 'verified' | 'fully_verified' | null
  verified_at: string | null
  created_at: string
  updated_at: string
  rating_avg: number | null
  review_count: number
  like_count: number
  recommend_count: number
  save_count: number
  is_open_now?: boolean | null
  distance_km?: number | null
  category_name?: string
  category_slug?: string
}

export interface VerificationDocumentDTO {
  id: string
  business_id: string
  kind: string
  media_id: string
  status: 'pending' | 'approved' | 'rejected' | 'expired'
  review_note: string | null
  reviewed_at: string | null
  created_at: string
  file_name?: string
}

export interface MediaDTO {
  id: string
  kind: string
  original_name: string
  mime: string
  size: number
  width: number | null
  height: number | null
  url: string
  thumb_url: string | null
  created_at: string
}

export interface SearchParams {
  q?: string
  city?: string
  category?: string[]
  lat?: number
  lng?: number
  radius_km?: number
  price_level?: number[]
  min_rating?: number
  open_now?: boolean
  verified_only?: boolean
  fully_verified_only?: boolean
  has_chat?: boolean
  sort?: 'trending' | 'rating' | 'newest' | 'nearest' | 'relevance'
  limit?: number
  offset?: number
  bbox?: string
}

export function searchPath(p: SearchParams): string {
  const qs = new URLSearchParams()
  if (p.q) qs.set('q', p.q)
  if (p.city) qs.set('city', p.city)
  for (const c of p.category ?? []) qs.append('category', c)
  if (p.lat !== undefined) qs.set('lat', String(p.lat))
  if (p.lng !== undefined) qs.set('lng', String(p.lng))
  if (p.radius_km) qs.set('radius_km', String(p.radius_km))
  if (p.bbox) qs.set('bbox', p.bbox)
  for (const pl of p.price_level ?? []) qs.append('price_level', String(pl))
  if (p.min_rating) qs.set('min_rating', String(p.min_rating))
  if (p.open_now) qs.set('open_now', 'true')
  if (p.verified_only) qs.set('verified_only', 'true')
  if (p.fully_verified_only) qs.set('fully_verified_only', 'true')
  if (p.has_chat) qs.set('has_chat', 'true')
  if (p.sort) qs.set('sort', p.sort)
  if (p.limit) qs.set('limit', String(p.limit))
  if (p.offset) qs.set('offset', String(p.offset))
  const s = qs.toString()
  return s ? `/search?${s}` : '/search'
}

export function uploadMedia(kind: string, file: File): Promise<{ media: MediaDTO }> {
  const fd = new FormData()
  fd.append('kind', kind)
  fd.append('file', file)
  return api('/media', { method: 'POST', body: fd })
}

export const DOC_KINDS: Record<string, string> = {
  registration: 'Business registration / license',
  tax_id: 'Tax ID',
  identity: 'Owner identity',
  utility: 'Utility bill (proof of address)',
}

export function mediaUrl(id: string | null | undefined): string | null {
  return id ? `/api/v1/media/${id}/file` : null
}

// ---- Products ----

export interface ProductDTO {
  id: string
  business_id: string
  type: 'product' | 'service'
  name: string
  description: string | null
  currency: string
  base_price: number | null
  call_for_price: boolean
  cover_image_id: string | null
  image_ids: string[]
  tags: string[]
  is_available: boolean
  is_featured: boolean
  sort_order: number
  is_published: boolean
  badge: 'none' | 'new' | 'popular'
  seo_title: string | null
  created_at: string
  updated_at: string
  options?: ProductOptionDTO[]
  variants?: ProductVariantDTO[]
}

export interface ProductOptionDTO {
  id: string
  product_id: string
  name: string
  values: string[]
  sort_order: number
}

export interface ProductVariantDTO {
  id: string
  product_id: string
  name: string
  sku: string
  options: Record<string, string>
  price: number | null
  currency: string
  stock_qty: number | null
  in_stock: boolean
  image_id: string | null
  sort_order: number
}

export interface AnalyticsDTO {
  period: string
  views: number
  likes: number
  recommends: number
  comments: number
  reviews: number
  saves: number
  chat_messages: number
  rating_avg: number | null
  top_products: { product_id: string; name: string; likes: number; views: number }[]
  views_series: { day: string; count: number }[]
  trend_score: number
  leaderboard: { global: number | null; category: number | null } | null
}

// ---- Engagement ----

export interface CollectionDTO {
  id: string
  user_id: string
  name: string
  slug: string
  is_public: boolean
  item_count: number
  created_at: string
}

export interface CollectionItemDTO {
  id: string
  collection_id: string
  target_type: string
  target_id: string
  note: string | null
  created_at: string
  target_name?: string
  target_slug?: string
  target_logo?: string
}

export interface CommentDTO {
  id: string
  business_id: string
  user_id: string
  parent_id: string | null
  text: string
  status: string
  created_at: string
  author_name: string
  author_username: string
  author_avatar: string | null
  like_count: number
  children?: CommentDTO[]
}

export interface ReviewDTO {
  id: string
  business_id: string
  product_id: string | null
  user_id: string
  rating: number
  text: string
  image_ids: string[]
  reply: string | null
  reply_at: string | null
  reply_edited_at?: string | null
  status: string
  created_at: string
  author_name: string
  author_username: string
  author_avatar: string | null
  helpful_count: number
  my_vote?: number | null
  business_name?: string
  business_slug?: string
}

export interface NotificationDTO {
  id: string
  user_id: string
  type: string
  payload: Record<string, unknown>
  is_read: boolean
  created_at: string
}

export interface TrendEntryDTO {
  id: string
  name: string
  slug: string
  logo_url: string | null
  city: string
  category: string | null
  score: number
  velocity: number
  is_booming: boolean
  is_rising: boolean
  verification_level: 'verified' | 'fully_verified' | null
}

// ---- Messaging ----

export interface ThreadListItemDTO {
  id: string
  type: 'direct' | 'business'
  business_id: string | null
  status: string
  last_message_at: string | null
  created_at: string
  business_name: string | null
  business_slug: string | null
  business_logo: string | null
  other_id: string | null
  other_name: string | null
  other_username: string | null
  other_avatar: string | null
  last_body: string | null
  unread: number
  pinned?: boolean
}

export interface ChatMessageDTO {
  id: number
  thread_id: string
  sender_id: string
  sender_role: string
  type: string
  body: string | null
  reply_to_id: number | null
  forwarded_from_message_id: number | null
  media_id: string | null
  link_preview: Record<string, unknown> | null
  client_msg_id: string
  read_count: number
  edited_at: string | null
  edit_history: { text: string | null; at: string }[]
  deleted_for: 'none' | 'me' | 'everyone'
  deleted_at: string | null
  created_at: string
}

export interface QuickReplyDTO {
  id: string
  business_id: string
  text: string
}
