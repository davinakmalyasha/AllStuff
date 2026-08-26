import { create } from 'zustand'
import { CheckCircle2, Info, AlertTriangle, XCircle, X } from 'lucide-react'

type ToastKind = 'success' | 'info' | 'warning' | 'error'

interface Toast {
  id: number
  kind: ToastKind
  message: string
}

interface ToastState {
  toasts: Toast[]
  push: (kind: ToastKind, message: string) => void
  dismiss: (id: number) => void
}

let nextId = 1

export const useToasts = create<ToastState>((set) => ({
  toasts: [],
  push: (kind, message) => {
    const id = nextId++
    set((s) => ({ toasts: [...s.toasts, { id, kind, message }] }))
    setTimeout(() => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })), 4000)
  },
  dismiss: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}))

export const toast = {
  success: (m: string) => useToasts.getState().push('success', m),
  info: (m: string) => useToasts.getState().push('info', m),
  warning: (m: string) => useToasts.getState().push('warning', m),
  error: (m: string) => useToasts.getState().push('error', m),
}

const ICONS: Record<ToastKind, typeof Info> = {
  success: CheckCircle2, info: Info, warning: AlertTriangle, error: XCircle,
}

/** Toast stack — replaces alert() everywhere (Batch 3). */
export function ToastStack() {
  const { toasts, dismiss } = useToasts()
  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-[100] flex w-80 flex-col gap-2">
      {toasts.map((t) => {
        const Icon = ICONS[t.kind]
        return (
          <div
            key={t.id}
            role={t.kind === 'error' ? 'alert' : 'status'}
            className="pointer-events-auto flex items-start gap-2 rounded-xl border border-border bg-surface px-4 py-3 shadow-cardHover animate-fadeUp"
          >
            <Icon className={`mt-0.5 h-4 w-4 shrink-0 ${t.kind === 'error' ? 'text-red-500' : t.kind === 'warning' ? 'text-amber-500' : 'text-ink'}`} />
            <p className="flex-1 text-sm text-ink">{t.message}</p>
            <button onClick={() => dismiss(t.id)} className="text-ink3 hover:text-ink" aria-label="Dismiss">
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        )
      })}
    </div>
  )
}
