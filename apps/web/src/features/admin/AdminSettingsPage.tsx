import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Megaphone } from 'lucide-react'
import { api } from '@/lib/api'
import { Card } from '@/components/ui/Card'
import { Button } from '@/components/ui/Button'
import { toast } from '@/components/ui/Toast'

/** Admin settings (PRD §5.8.5): announcement banner + platform toggles. */
export function AdminSettingsPage() {
  const qc = useQueryClient()
  const [announcement, setAnnouncement] = useState('')
  const [announcementEnabled, setAnnouncementEnabled] = useState(true)

  const { data } = useQuery({
    queryKey: ['admin-settings'],
    queryFn: () => api<{ announcement?: string; announcement_enabled?: boolean }>('/admin/settings'),
  })

  useEffect(() => {
    if (typeof data?.announcement === 'string') setAnnouncement(data.announcement)
    if (typeof data?.announcement_enabled === 'boolean') setAnnouncementEnabled(data.announcement_enabled)
  }, [data])

  const save = useMutation({
    mutationFn: (body: Record<string, unknown>) => api('/admin/settings', { method: 'PUT', body }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin-settings'] })
      toast.success('Settings saved')
    },
  })

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <div>
        <p className="mono-label mb-1">Platform</p>
        <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
      </div>

      <Card className="space-y-4">
        <div className="flex items-center gap-2">
          <Megaphone className="h-4 w-4 text-ink2" />
          <p className="text-sm font-semibold text-ink">Announcement banner</p>
          <label className="ml-auto flex items-center gap-2 text-sm text-ink2">
            <input
              type="checkbox"
              checked={announcementEnabled}
              onChange={(e) => setAnnouncementEnabled(e.target.checked)}
              className="h-3.5 w-3.5 accent-black dark:accent-white"
            />
            Visible
          </label>
        </div>
        <textarea
          rows={3}
          value={announcement}
          onChange={(e) => setAnnouncement(e.target.value)}
          placeholder="e.g. Scheduled maintenance Saturday 02:00–04:00 UTC"
          className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink"
        />
        <div className="flex justify-end">
          <Button size="sm" onClick={() => void save.mutateAsync({ announcement, announcement_enabled: announcementEnabled })} disabled={save.isPending}>
            Save
          </Button>
        </div>
      </Card>
    </div>
  )
}
