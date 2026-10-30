"use client"

// Port of the Bootstrap side-effects in apps/web/src/App.tsx (error reporter,
// compare hydration, tab-title unread count, keyboard shortcut, back-to-top,
// single WS connection). Kept behavior-identical; only the router import is
// gone (pathname comes from next/navigation).

import { useEffect, useState } from 'react'
import { usePathname } from 'next/navigation'
import { useQuery } from '@tanstack/react-query'
import { ws } from '@/lib/ws'
import { api } from '@/lib/api'
import { useAuthState } from '@/stores/auth'
import { hydrateCompare } from '@/stores/compare'

function LiveEvents() {
  const { user } = useAuthState((s) => ({ user: s.user }))
  const userId = user?.id ?? null
  useEffect(() => {
    if (!userId) return
    ws.connect()
    return () => ws.close()
  }, [userId])
  return null
}

function TabTitle() {
  const { user } = useAuthState((s) => ({ user: s.user }))
  const { data: notif } = useQuery({
    queryKey: ['notifications', 'count'],
    queryFn: () => api<{ unread: number }>('/notifications?limit=1'),
    enabled: !!user,
    refetchInterval: 60_000,
  })
  const { data: threads } = useQuery({
    queryKey: ['threads', 'all'],
    queryFn: () => api<{ threads: Array<{ unread: number }> }>('/threads'),
    enabled: !!user,
    refetchInterval: 60_000,
  })
  useEffect(() => {
    const unread = (notif?.unread ?? 0) + (threads?.threads ?? []).reduce((a, t) => a + t.unread, 0)
    const base = document.title.replace(/^\(\d+\) /, '')
    document.title = unread > 0 ? `(${unread}) ${base}` : base
  }, [notif, threads])
  return null
}

function KeyboardShortcuts() {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = document.activeElement
      if (e.key === '/' && el?.tagName !== 'INPUT' && el?.tagName !== 'TEXTAREA') {
        e.preventDefault()
        document.querySelector<HTMLInputElement>('input[type="search"], input[placeholder*="Search" i]')?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  return null
}

function BackToTop() {
  const [show, setShow] = useState(false)
  useEffect(() => {
    const onScroll = () => setShow(window.scrollY > 600)
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => window.removeEventListener('scroll', onScroll)
  }, [])
  if (!show) return null
  return (
    <button
      onClick={() => window.scrollTo({ top: 0, behavior: 'smooth' })}
      className="fixed bottom-16 right-4 z-40 flex h-10 w-10 items-center justify-center rounded-full border border-border bg-surface text-ink shadow-cardHover hover:bg-surface2"
      aria-label="Back to top"
    >
      ↑
    </button>
  )
}

export function LiveIsland() {
  const pathname = usePathname()
  const { fetchMe, user } = useAuthState((s) => ({ fetchMe: s.fetchMe, user: s.user }))
  useEffect(() => {
    // Marks hydration commit for tests and debugging: SSR HTML never carries
    // this attribute (it is set imperatively post-mount), so its presence
    // proves the client tree took over rather than merely that SSR bytes
    // arrived. The a11y audit waits on it instead of a framework mount node
    // (`#__next` does not exist in Next 16 output; `#root` was the Vite id).
    try {
      document.body.dataset.hydrated = '1'
    } catch {
      /* noop */
    }
    void fetchMe()
    if ('serviceWorker' in navigator) {
      navigator.serviceWorker
        .register('/sw.js')
        .then((reg) => {
          reg.addEventListener('updatefound', () => {
            const installing = reg.installing
            if (!installing) return
            installing.addEventListener('statechange', () => {
              if (installing.state === 'activated' && navigator.serviceWorker.controller) {
                import('@/components/ui/Toast').then(({ toast }) =>
                  toast.info('A new version is available — refresh to update.'),
                )
              }
            })
          })
        })
        .catch(() => undefined)
    }
    const orig = window.onerror
    window.onerror = (msg, src, line, col, err) => {
      try {
        void fetch('/api/v1/errors', {
          method: 'POST',
          credentials: 'include',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ message: String(msg), stack: (err as Error)?.stack ?? `${src}:${line}:${col}`, url: window.location.origin + window.location.pathname }),
        })
      } catch {
        /* noop */
      }
      return orig?.(msg, src, line, col, err) ?? false
    }
    return () => {
      window.onerror = orig
    }
  }, [fetchMe])
  useEffect(() => {
    window.scrollTo(0, 0)
  }, [pathname])
  useEffect(() => {
    if (user) void hydrateCompare()
  }, [user])
  return (
    <>
      <TabTitle />
      <KeyboardShortcuts />
      <BackToTop />
      <LiveEvents />
    </>
  )
}
