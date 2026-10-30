import type { NextConfig } from 'next'

const API_ORIGIN = process.env.API_ORIGIN ?? 'http://localhost:8080'

// Fail loud in production when API_ORIGIN is unset: silently proxying to
// localhost in a deployed environment ships a broken app. Local dev keeps the
// localhost default so `npm run dev` works with no env.
if (process.env.NODE_ENV === 'production' && !process.env.API_ORIGIN) {
  console.warn('[web-next] API_ORIGIN unset; proxying to http://localhost:8080')
}

const nextConfig: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  async rewrites() {
    return [
      // Browser calls same-origin /api/v1/*; Next proxies to Go. Cookies stay
      // first-party. WS upgrade is handled by the platform (Vercel) via the
      // vercel.json WS-first rule; local dev uses NEXT_PUBLIC_WS_BASE directly.
      { source: '/api/:path*', destination: `${API_ORIGIN}/api/:path*` },
      // Go remains the source of truth for SEO-adjacent files (proxied
      // verbatim); page HTML/OG/JSON-LD is rendered by Next itself.
      { source: '/og/:path*', destination: `${API_ORIGIN}/og/:path*` },
      { source: '/robots.txt', destination: `${API_ORIGIN}/robots.txt` },
      { source: '/sitemap.xml', destination: `${API_ORIGIN}/api/v1/sitemap.xml` },
    ]
  },
  async headers() {
    // Real headers (not <meta> CSP): frame-ancestors is ignored in <meta> and
    // must be delivered here. Nonce for Next hydration scripts is added in
    // proxy.ts; this static block carries everything else.
    // NOTE: no script-src here — proxy.ts sets the full CSP with per-request
    // nonce. Setting it in both places would send two policies (intersection).
    return [
      {
        source: '/(.*)',
        headers: [
          { key: 'X-Content-Type-Options', value: 'nosniff' },
          { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
          { key: 'X-Frame-Options', value: 'DENY' },
          { key: 'Strict-Transport-Security', value: 'max-age=63072000; includeSubDomains; preload' },
          {
            key: 'Permissions-Policy',
            value: 'camera=(), microphone=(self), geolocation=(self), payment=(), usb=(), interest-cohort=()',
          },
          { key: 'Cross-Origin-Opener-Policy', value: 'same-origin' },
          { key: 'Cross-Origin-Resource-Policy', value: 'same-origin' },
        ],
      },
      {
        source: '/(.*).map',
        headers: [{ key: 'Cache-Control', value: 'no-store' }],
      },
    ]
  },
  images: {
    // Business/user uploads are served by the Go API origin.
    remotePatterns: [{ protocol: 'http', hostname: 'localhost' }],
  },
}

export default nextConfig
