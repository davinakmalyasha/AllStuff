import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'

// Where the dev/preview server forwards /api and /ws.
//
// Overridable so this suite can run beside other work on the same machine.
// The default is unchanged, so CI and a normal `npm run dev` behave exactly as
// before; only a caller that sets it gets a different port. Hardcoding it meant
// that anyone already holding 8080 — another project, or a stale server — had no
// way to run the frontend against their own API without editing this file.
const API_TARGET = process.env.VITE_API_TARGET ?? 'http://localhost:8080'
const WS_TARGET = API_TARGET.replace(/^http/, 'ws')

const proxy = {
  '/api': { target: API_TARGET, changeOrigin: true },
  '/ws': { target: WS_TARGET, ws: true },
  // The SSR and SEO routes are served by the Go API at the ORIGIN ROOT, not
  // under /api/v1 — see docs/ARCHITECTURE.md. They were missing here, so in dev
  // (and in the E2E suite, whose baseURL is this server) `/ssr/b/{slug}` fell
  // through to the SPA shell and the crawlers got an empty document. The
  // Playwright assertion for the JSON-LD surface failed against that shell
  // while the API itself served a correct page, which reads as a product bug and
  // is not one.
  '/ssr': { target: API_TARGET, changeOrigin: true },
  '/og': { target: API_TARGET, changeOrigin: true },
  '/robots.txt': { target: API_TARGET, changeOrigin: true },
  '/sitemap.xml': { target: API_TARGET, changeOrigin: true },
}

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': path.resolve(__dirname, 'src') },
  },
  server: {
    port: 5173,
    proxy,
  },
  optimizeDeps: {
    exclude: ['maplibre-gl'],
  },
  preview: {
    port: 4173,
    proxy,
  },
  build: {
    outDir: 'dist',
    // Emit .vite/manifest.json so the API's server-rendered SEO shell
    // (services/api/internal/httpapi/handlers_ssr.go) can reference the
    // hashed entry script and its CSS without hardcoding filenames. Without
    // this the shell can only serve static HTML with no hydration.
    manifest: true,
    // Never ship public source maps. `sourcemap: true` was publishing the full
    // original TypeScript (6.3 MB of the 6.35 MB dist/assets) at
    // /assets/*.map, handing an attacker every API path, the validation model
    // and the open-redirect allowlist.
    //
    // Use `hidden` (maps emitted, no //# sourceMappingURL comment) and upload
    // them to an error tracker instead:
    //   SENTRY_AUTH_TOKEN=... SOURCEMAP=hidden npm run build
    sourcemap: process.env.SOURCEMAP === 'true' ? 'inline' : process.env.SOURCEMAP === 'hidden' ? 'hidden' : false,
    rollupOptions: {
      output: {
        // Without this, React, the router, TanStack Query, Zustand and BOTH
        // locale JSON files are inlined into a single 139 KB gzip entry chunk
        // that is invalidated on every app deploy. Splitting vendor lets it be
        // cached across deploys.
        manualChunks(id) {
          if (!id.includes('node_modules')) return
          if (id.includes('maplibre-gl')) return 'map'
          if (/[\\/]node_modules[\\/](react|react-dom|scheduler)[\\/]/.test(id)) return 'react'
          if (id.includes('react-router')) return 'router'
          if (id.includes('@tanstack')) return 'query'
          if (id.includes('zustand')) return 'state'
          if (id.includes('i18next') || id.includes('i18n')) return 'i18n'
          return 'vendor'
        },
      },
    },
    // The map chunk is inherently large; the default 500 kB warning is noise.
    chunkSizeWarningLimit: 1100,
  },
})
