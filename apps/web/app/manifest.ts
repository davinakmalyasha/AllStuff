import type { MetadataRoute } from 'next'

// Ports public/manifest.webmanifest from the Vite app verbatim (same id,
// shortcuts, icon set) so installed-PWA behavior is identical.

export default function manifest(): MetadataRoute.Manifest {
  return {
    id: '/',
    name: 'AllStuff — Every business. One place.',
    short_name: 'AllStuff',
    description: 'Every business in the world, in one place.',
    start_url: '/',
    display: 'standalone',
    background_color: '#ffffff',
    theme_color: '#ffffff',
    categories: ['business', 'food', 'shopping'],
    shortcuts: [
      {
        name: 'Discover',
        url: '/discover',
        icons: [{ src: '/icon-192.svg', sizes: '192x192', type: 'image/svg+xml' }],
      },
      {
        name: 'Map',
        url: '/map',
        icons: [{ src: '/icon-192.svg', sizes: '192x192', type: 'image/svg+xml' }],
      },
      {
        name: 'Compare',
        url: '/compare',
        icons: [{ src: '/icon-192.svg', sizes: '192x192', type: 'image/svg+xml' }],
      },
    ],
    icons: [
      { src: '/icon-192.png', sizes: '192x192', type: 'image/png' },
      { src: '/icon-512.png', sizes: '512x512', type: 'image/png' },
      { src: '/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
      { src: '/icon-192.svg', sizes: '192x192', type: 'image/svg+xml' },
      { src: '/icon-512.svg', sizes: '512x512', type: 'image/svg+xml' },
    ],
  }
}
