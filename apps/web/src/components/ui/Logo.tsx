import { Link } from 'react-router-dom'

export function Logo({ className = '' }: { className?: string }) {
  return (
    <Link
      to="/"
      className={`inline-flex items-center gap-2 font-semibold tracking-tight text-ink ${className}`}
      aria-label="BizVerse home"
    >
      <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-accent">
        <svg viewBox="0 0 64 64" className="h-4 w-4" aria-hidden>
          <path d="M18 46V28l10 12V20l10 20V26l8 6V46h-8v-8l-4 4-6-10v14z" fill="var(--color-accent-ink)" />
        </svg>
      </span>
      <span className="font-mono text-sm uppercase tracking-[0.14em]">
        Biz<span className="text-ink3">Verse</span>
      </span>
    </Link>
  )
}
