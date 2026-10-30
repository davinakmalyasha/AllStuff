// Server-side API helper for React Server Components. Mirrors the envelope
// contract of src/lib/api.ts (PRD §11.2) but forwards the incoming Cookie
// header instead of relying on document.cookie. No CSRF header needed for
// GETs; mutations from RSC are not performed (client islands do writes).

import { cookies } from 'next/headers'

const API_INTERNAL = process.env.API_INTERNAL_URL ?? process.env.API_ORIGIN ?? 'http://localhost:8080'

export interface ApiErrorBody {
  error: { code: string; message: string; fields?: Record<string, string> }
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

interface FetchOpts extends RequestInit {
  revalidate?: number | false
  /** Skip forwarding cookies (public SEO fetches). Without this, cookies()
   * opts the page into dynamic rendering and ISR is lost. */
  auth?: boolean
}

export async function apiServer<T>(path: string, opts: FetchOpts = {}): Promise<T> {
  const { auth = true, revalidate, ...init } = opts
  let cookieHeader = ''
  if (auth) {
    const cookieStore = await cookies()
    cookieHeader = cookieStore
      .getAll()
      .map((c) => `${c.name}=${c.value}`)
      .join('; ')
  }
  const res = await fetch(`${API_INTERNAL}/api/v1${path}`, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(cookieHeader ? { Cookie: cookieHeader } : {}),
      ...(opts.headers ?? {}),
    },
    next: revalidate !== undefined ? { revalidate: revalidate as number | false } : undefined,
  })
  if (res.status === 204) return undefined as T
  let data: unknown = null
  try {
    data = await res.json()
  } catch {
    /* empty */
  }
  if (!res.ok) {
    const err = (data as ApiErrorBody | null)?.error
    throw new ApiError(err?.code ?? 'http_error', err?.message ?? `Request failed (${res.status}).`, res.status, err?.fields)
  }
  return data as T
}

export async function getSessionUser(): Promise<{ id: string; is_admin: boolean } | null> {
  try {
    return await apiServer('/me', { revalidate: false })
  } catch {
    return null
  }
}

export function siteUrl(): string {
  return process.env.SITE_URL ?? process.env.PUBLIC_URL ?? 'http://localhost:3000'
}
