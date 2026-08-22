import { create } from 'zustand'
import { api } from '@/lib/api'

interface CompareState {
  ids: string[]
  toggle: (id: string) => void
  clear: () => void
  setIds: (ids: string[]) => void
}

const KEY = 'bv.compare'

function read(): string[] {
  try {
    const raw = localStorage.getItem(KEY)
    if (raw) return JSON.parse(raw) as string[]
  } catch {
    /* noop */
  }
  return []
}

function write(ids: string[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(ids))
  } catch {
    /* noop */
  }
}

// Account sync (PRD §5.1.5): guests persist locally; signed-in users also
// mirror the tray to their profile so it follows them across devices.
let signedIn = false
let pushTimer: ReturnType<typeof setTimeout> | null = null

function pushRemote(ids: string[]) {
  if (!signedIn) return
  if (pushTimer) clearTimeout(pushTimer)
  pushTimer = setTimeout(() => {
    void api('/me/compare', { method: 'PUT', body: { ids } }).catch(() => undefined)
  }, 800)
}

/** Pull the server-side tray after login and merge with local state. */
export async function hydrateCompare(): Promise<void> {
  try {
    const r = await api<{ ids: string[] }>('/me/compare')
    signedIn = true
    const local = read()
    const merged = [...new Set([...(r.ids ?? []), ...local])].slice(0, 4)
    useCompare.setState({ ids: merged })
    write(merged)
    pushRemote(merged)
  } catch {
    signedIn = false
  }
}

/** Compare tray (PRD §5.1.5): max 4, persists across routes, sessions, devices. */
export const useCompare = create<CompareState>((set) => ({
  ids: read(),
  toggle: (id) =>
    set((s) => {
      const ids = s.ids.includes(id) ? s.ids.filter((x) => x !== id) : s.ids.length >= 4 ? [...s.ids.slice(1), id] : [...s.ids, id]
      write(ids)
      pushRemote(ids)
      return { ids }
    }),
  clear: () => {
    write([])
    pushRemote([])
    set({ ids: [] })
  },
  setIds: (ids) => {
    write(ids)
    pushRemote(ids)
    set({ ids })
  },
}))
