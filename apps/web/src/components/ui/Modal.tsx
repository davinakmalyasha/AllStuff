import { useEffect, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'

/** Accessible modal: focus trap, Escape to close, body scroll lock (Batch 3). */
export function Modal({
  open,
  onClose,
  title,
  children,
  maxWidth = 'max-w-lg',
}: {
  open: boolean
  onClose: () => void
  title?: string
  children: ReactNode
  maxWidth?: string
}) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    ref.current?.focus()
    return () => {
      document.body.style.overflow = prev
      window.removeEventListener('keydown', onKey)
    }
  }, [open, onClose])

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 backdrop-blur-sm"
      onClick={onClose}
    >
      <div
        ref={ref}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onClick={(e) => e.stopPropagation()}
        className={`my-8 w-full ${maxWidth} rounded-xl border border-border bg-surface shadow-cardHover outline-none`}
      >
        <div className="flex items-start justify-between border-b border-border px-5 py-4">
          <h2 className="text-base font-semibold tracking-tight text-ink">{title}</h2>
          <button onClick={onClose} className="rounded-lg p-1 text-ink3 hover:bg-surface2 hover:text-ink" aria-label="Close">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="px-5 py-4">{children}</div>
      </div>
    </div>
  )
}

/** Confirm dialog built on Modal — replaces window.confirm. */
export function Confirm({
  open,
  onClose,
  onConfirm,
  title,
  message,
  confirmLabel = 'Confirm',
  danger = false,
}: {
  open: boolean
  onClose: () => void
  onConfirm: () => void
  title: string
  message: string
  confirmLabel?: string
  danger?: boolean
}) {
  return (
    <Modal open={open} onClose={onClose} title={title} maxWidth="max-w-sm">
      <p className="text-sm text-ink2">{message}</p>
      <div className="mt-4 flex justify-end gap-2">
        <button onClick={onClose} className="rounded-lg border border-border px-3 py-2 text-sm text-ink hover:bg-surface2">Cancel</button>
        <button
          onClick={() => {
            onConfirm()
            onClose()
          }}
          className={`rounded-lg px-3 py-2 text-sm font-medium ${danger ? 'bg-red-600 text-white hover:bg-red-700' : 'bg-accent text-accent-ink'}`}
        >
          {confirmLabel}
        </button>
      </div>
    </Modal>
  )
}
