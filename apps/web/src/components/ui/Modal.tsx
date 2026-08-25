import { useEffect, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'

/** Escape-to-close + focus management for bespoke overlays: focuses the
 * returned ref on open and restores the previously focused element on close.
 * onClose is kept in a ref so identity churn across renders doesn't re-run
 * the effect (which would steal focus back from inner inputs). */
export function useDialogA11y(open: boolean, onClose: () => void) {
  const ref = useRef<HTMLDivElement>(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  useEffect(() => {
    if (!open) return
    const prevFocus = document.activeElement as HTMLElement | null
    ref.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeRef.current()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('keydown', onKey)
      prevFocus?.focus?.()
    }
  }, [open])
  return ref
}

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
    const prevFocus = document.activeElement as HTMLElement | null
    document.body.style.overflow = 'hidden'
    ref.current?.focus()
    return () => {
      document.body.style.overflow = prev
      prevFocus?.focus?.()
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    // Focus trap: keep Tab cycling inside the dialog (WAI-ARIA modal pattern).
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onClose()
        return
      }
      if (e.key !== 'Tab' || !ref.current) return
      const focusables = ref.current.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), textarea, input, select, [tabindex]:not([tabindex="-1"])',
      )
      if (focusables.length === 0) {
        e.preventDefault()
        return
      }
      const first = focusables[0]
      const last = focusables[focusables.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
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
