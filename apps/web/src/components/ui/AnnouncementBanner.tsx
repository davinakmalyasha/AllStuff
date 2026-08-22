import { useQuery } from '@tanstack/react-query'
import { Megaphone } from 'lucide-react'
import { api } from '@/lib/api'

/** Announcement banner from site config (PRD §5.8.5). */
export function AnnouncementBanner() {
  const { data } = useQuery({
    queryKey: ['meta'],
    queryFn: () => api<{ site: Record<string, unknown> }>('/meta'),
    staleTime: 5 * 60_000,
  })
  const text = data?.site?.announcement
  const enabled = data?.site?.announcement_enabled !== false
  if (typeof text !== 'string' || !text.trim() || !enabled) return null
  return (
    <div className="border-b border-border bg-surface2 px-4 py-2 text-center text-xs text-ink2">
      <Megaphone className="mr-1.5 inline h-3 w-3" aria-hidden />
      {text}
    </div>
  )
}
