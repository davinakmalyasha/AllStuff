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
// Only merge LOCAL items into the server tray if they were added while
// logged out this session. Without this gate, user B logging in after user
// A logged out inherited A's tray and wrote it into B's profile.
let guestDirty = read().length > 0
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
    const merged = guestDirty
      ? [...new Set([...(r.ids ?? []), ...read()])].slice(0, 4)
      : (r.ids ?? []).slice(0, 4)
    guestDirty = false
    useCompare.setState({ ids: merged })
    write(merged)
    pushRemote(merged)
  } catch {
    signedIn = false
  }
}

/**
 * Logout hygiene: the tray (and its storage) belonged to the account that
 * just left. Reset sync state and wipe local items so the next user on a
 * shared machine starts clean.
 */
export function resetCompareSession(): void {
  signedIn = false
  guestDirty = false
  write([])
  useCompare.setState({ ids: [] })
}

/** Compare tray (PRD §5.1.5): max 4, persists across routes, sessions, devices. */
export const useCompare = create<CompareState>((set) => ({
  ids: read(),
  toggle: (id) =>
    set((s) => {
      const ids = s.ids.includes(id) ? s.ids.filter((x) => x !== id) : s.ids.length >= 4 ? [...s.ids.slice(1), id] : [...s.ids, id]
      if (!signedIn) guestDirty = true
      write(ids)
      pushRemote(ids)
      return { ids }
    }),
  clear: () => {
    if (!signedIn) guestDirty = true
    write([])
    pushRemote([])
    set({ ids: [] })
  },
  setIds: (ids) => {
    if (!signedIn) guestDirty = true
    write(ids)
    pushRemote(ids)
    set({ ids })
  },
}))
