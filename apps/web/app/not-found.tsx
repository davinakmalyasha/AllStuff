import Link from 'next/link'

export default function NotFound() {
  return (
    <div className="container-page flex min-h-[50vh] flex-col items-center justify-center gap-3 text-center">
      <p className="font-mono text-5xl font-semibold tracking-tight">404</p>
      <p className="text-sm text-ink2">This page doesn&apos;t exist or may have moved.</p>
      <div className="mt-2 flex gap-2">
        <Link
          href="/"
          className="inline-flex h-8 items-center rounded-lg border border-border bg-surface px-3 text-sm hover:bg-surface2"
        >
          Home
        </Link>
        <Link
          href="/discover"
          className="inline-flex h-8 items-center rounded-lg border border-border bg-surface px-3 text-sm hover:bg-surface2"
        >
          Discover
        </Link>
      </div>
    </div>
  )
}
