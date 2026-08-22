import { create } from 'zustand'

interface CompareState {
  ids: string[]
  toggle: (id: string) => void
  clear: () => void
  setIds: (ids: string[]) => void
}

const KEY = 'bv.compare'

function read(): string[] {
  try {
    const raw = sessionStorage.getItem(KEY)
    if (raw) return JSON.parse(raw) as string[]
  } catch {
    /* noop */
  }
  return []
}

/** Compare tray (PRD §5.1.5): max 4, cross-route persistence. */
export const useCompare = create<CompareState>((set) => ({
  ids: read(),
  toggle: (id) =>
    set((s) => {
      const ids = s.ids.includes(id) ? s.ids.filter((x) => x !== id) : s.ids.length >= 4 ? [...s.ids.slice(1), id] : [...s.ids, id]
      try {
        sessionStorage.setItem(KEY, JSON.stringify(ids))
      } catch {
        /* noop */
      }
      return { ids }
    }),
  clear: () => {
    try {
      sessionStorage.removeItem(KEY)
    } catch {
      /* noop */
    }
    set({ ids: [] })
  },
  setIds: (ids) => set({ ids }),
}))
