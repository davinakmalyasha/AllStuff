import i18n from '@/lib/i18n'

/** Capitalize the first letter ("monday" → "Monday"). */
export function cap(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

/** Font stacks for storefront theming (shared by BusinessPage + preview). */
export const FONTS: Record<string, string> = {
  inter: "'Inter', system-ui, sans-serif",
  serif: 'Georgia, "Times New Roman", serif',
  mono: 'ui-monospace, SFMono-Regular, Menlo, monospace',
}

/** Card skeleton (Batch 3). */
export function SkeletonCard() {
  return (
    <div className="card p-4">
      <div className="h-14 w-14 animate-pulse rounded-xl bg-surface2" />
      <div className="mt-3 h-4 w-3/4 animate-pulse rounded bg-surface2" />
      <div className="mt-2 h-3 w-1/2 animate-pulse rounded bg-surface2" />
    </div>
  )
}

/** Locale-aware date/time formatting unified across the app; follows the
 * current i18n language so en/id users see their own conventions. */
function intlLocale(): string {
  return i18n.language?.startsWith('id') ? 'id-ID' : 'en-US'
}

export function formatDate(d: string | Date): string {
  const date = typeof d === 'string' ? new Date(d) : d
  return date.toLocaleDateString(intlLocale())
}

export function formatDateTime(d: string | Date): string {
  const date = typeof d === 'string' ? new Date(d) : d
  return date.toLocaleString(intlLocale())
}

/** Reset a file input after reading its file so picking the SAME file again
 * re-fires onChange (browsers skip change events when value is unchanged). */
export function resetFileInput(e: React.ChangeEvent<HTMLInputElement>) {
  e.target.value = ''
}

/** Copy to clipboard with feedback (Batch 1 share fix). */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    // Fallback for non-secure contexts.
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
      return true
    } catch {
      return false
    }
  }
}
