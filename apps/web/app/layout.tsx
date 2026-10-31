import type { Metadata, Viewport } from 'next'
import Script from 'next/script'
import { headers } from 'next/headers'
import { Providers } from '@/components/next-providers'
import './globals.css'

const SITE = process.env.SITE_URL ?? process.env.PUBLIC_URL ?? 'http://localhost:3000'

export const metadata: Metadata = {
  metadataBase: new URL(SITE),
  title: { default: 'AllStuff — Every business. One place.', template: '%s · AllStuff' },
  description: 'Every business in the world, in one place. Find, compare, and contact any business.',
  openGraph: {
    type: 'website',
    siteName: 'AllStuff',
    title: 'AllStuff — Every business. One place.',
    description: 'Every business in the world, in one place. Find, compare, and contact any business.',
    images: [{ url: '/og-default.png', width: 1200, height: 630 }],
    locale: 'en_US',
    alternateLocale: ['id_ID'],
  },
  twitter: { card: 'summary_large_image' },
  robots: { index: true, follow: true },
}

export const viewport: Viewport = {
  themeColor: [
    { media: '(prefers-color-scheme: light)', color: '#ffffff' },
    { media: '(prefers-color-scheme: dark)', color: '#0a0a0a' },
  ],
}

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const h = await headers()
  const nonce = h.get('x-nonce') ?? undefined
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        {/* Same-origin theme init (no inline script: static script-src stays intact). */}
        <Script src="/theme-init.js" strategy="beforeInteractive" nonce={nonce} />
      </head>
      <body>
        <Providers>{children}</Providers>
      </body>
    </html>
  )
}
