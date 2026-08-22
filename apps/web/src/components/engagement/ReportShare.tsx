import { useState } from 'react'
import { Flag, Share2 } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { toast } from '@/components/ui/Toast'
import { copyText } from '@/lib/format'

/** Share — native share with copy fallback (Batch 1 fix). */
export function ShareButton({ slug, name }: { slug: string; name: string }) {
  const share = async () => {
    const url = `${window.location.origin}/b/${slug}`
    if (navigator.share) {
      try {
        await navigator.share({ title: name, url })
        return
      } catch {
        /* user cancelled — fall through */
      }
    }
    if (await copyText(url)) toast.success('Link copied')
    else toast.error('Could not copy link')
  }
  return (
    <Button variant="secondary" onClick={() => void share()}>
      <Share2 className="h-4 w-4" /> Share
    </Button>
  )
}

/** Report — works for business, review, comment, product (Batch 1 fix). */
export function ReportButton({ targetType, targetId, compact = false }: { targetType: string; targetId: string; compact?: boolean }) {
  const [open, setOpen] = useState(false)
  const [reason, setReason] = useState('')
  const [pending, setPending] = useState(false)

  const submit = async () => {
    setPending(true)
    try {
      await api('/reports', { method: 'POST', body: { target_type: targetType, target_id: targetId, reason } })
      setOpen(false)
      setReason('')
      toast.success('Reported. Our team will review it.')
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setPending(false)
    }
  }

  if (compact) {
    return (
      <>
        <button onClick={() => setOpen(true)} className="text-[10px] text-ink3 hover:text-ink" aria-label="Report">
          <Flag className="inline h-2.5 w-2.5" /> report
        </button>
        <Modal open={open} onClose={() => setOpen(false)} title="Report this content">
          <ReportForm reason={reason} setReason={setReason} pending={pending} submit={submit} />
        </Modal>
      </>
    )
  }

  return (
    <>
      <Button variant="secondary" onClick={() => setOpen(true)}>
        <Flag className="h-4 w-4" /> Report
      </Button>
      <Modal open={open} onClose={() => setOpen(false)} title="Report this content">
        <ReportForm reason={reason} setReason={setReason} pending={pending} submit={submit} />
      </Modal>
    </>
  )
}

function ReportForm({ reason, setReason, pending, submit }: { reason: string; setReason: (v: string) => void; pending: boolean; submit: () => void }) {
  return (
    <div className="space-y-3">
      <p className="text-sm text-ink2">Tell us what's wrong. Reports are reviewed by the platform team.</p>
      <textarea
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        rows={4}
        placeholder="e.g. Incorrect information, spam, harassment…"
        className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-ink3 focus:border-ink"
      />
      <div className="flex justify-end">
        <Button size="sm" onClick={() => void submit()} disabled={reason.trim().length < 3 || pending}>
          Submit report
        </Button>
      </div>
    </div>
  )
}
