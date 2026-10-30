import { NextRequest, NextResponse } from 'next/server'

// Next 16 entry point (replaces middleware.ts). Two jobs:
// 1. UX-gate redirects for protected areas (the API remains the authority).
// 2. Per-request CSP nonce so Next hydration/RSC scripts pass a strict
//    script-src (a static 'self' policy would break the app).

function makeNonce(): string {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  let s = ''
  for (const b of bytes) s += String.fromCharCode(b)
  return btoa(s)
}

export default function proxy(request: NextRequest) {
  const nonce = makeNonce()
  const { pathname } = request.nextUrl

  // UX gate only: presence of the access cookie. Real authorization happens
  // server-side per layout (GET /me) and on every Go API call.
  const hasSession = request.cookies.has('bv_access') || request.cookies.has('access_token')
  const isProtected = pathname === '/me' || pathname.startsWith('/me/') || pathname.startsWith('/dashboard') || pathname.startsWith('/admin')
  const isGuestOnly =
    pathname === '/login' || pathname === '/register' || pathname === '/2fa' || pathname === '/auth/restore'
  if (isProtected && !hasSession) {
    const url = request.nextUrl.clone()
    url.pathname = '/login'
    url.searchParams.set('next', pathname)
    return redirectWithNonce(url, nonce, request)
  }
  if (isGuestOnly && hasSession) {
    const url = request.nextUrl.clone()
    url.pathname = '/me'
    url.search = ''
    return redirectWithNonce(url, nonce, request)
  }

  const res = NextResponse.next()
  res.headers.set('x-nonce', nonce)
  res.headers.set('Content-Security-Policy', csp(nonce, request.nextUrl.hostname))
  return res
}

function redirectWithNonce(url: URL, nonce: string, request: NextRequest) {
  const res = NextResponse.redirect(url)
  res.headers.set('x-nonce', nonce)
  res.headers.set('Content-Security-Policy', csp(nonce, request.nextUrl.hostname))
  return res
}

function csp(nonce: string, _host: string): string {
  const dev = process.env.NODE_ENV === 'development' ? " 'unsafe-eval'" : ''
  return [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}'${dev}`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob: https://tiles.openstreetmap.org https://*.basemaps.cartocdn.com https://*.tile.openstreetmap.org",
    // maplibre-gl v6 fetches tiles via Fetch/XHR, which is governed by
    // connect-src, not img-src. Without these origins every tile is a CSP
    // violation (27 console errors on the business page in the Vite app,
    // which carries the same img-src list but no connect-src tile origins).
    "connect-src 'self' ws: wss: https://tiles.openstreetmap.org https://*.basemaps.cartocdn.com https://*.tile.openstreetmap.org",
    "font-src 'self' data:",
    "object-src 'none'",
    "frame-src 'none'",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "worker-src 'self' blob:",
    'manifest-src \'self\'',
    'upgrade-insecure-requests',
  ].join('; ')
}

export const config = {
  matcher: ['/((?!_next/static|_next/image|favicon.svg|icon-|sw.js|manifest.webmanifest|og-default.png).*)'],
}
