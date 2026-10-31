"use client"

import { useEffect, useRef, useState, type ReactNode } from 'react'

/**
 * Defers mounting a below-fold section until it is near the viewport. The
 * section components fetch on mount, so an eagerly mounted page paid for
 * reviews, comments, Q&A and updates in four requests before the reader had
 * scrolled to any of them.
 *
 * `id` exists for deep links: /b/<slug>?review=1#reviews must find its target,
 * so a section whose id matches the current hash mounts immediately instead of
 * waiting for an intersection that never comes (the browser cannot scroll to an
 * element that does not exist yet).
 */
export function LazySection({
  children,
  id,
  minHeight = 160,
}: {
  children: ReactNode
  id?: string
  minHeight?: number
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [visible, setVisible] = useState(false)
  const mounted = useRef(false)

  useEffect(() => {
    if (visible || mounted.current) return
    // A deep link must win over the intersection: the browser cannot scroll to
    // a section that is not mounted. Checked in an effect, not in the state
    // initializer, so SSR and the first client render agree.
    if (id && window.location.hash === `#${id}`) {
      setVisible(true)
      return
    }
    const el = ref.current
    if (!el) return
    if (typeof IntersectionObserver === 'undefined') {
      setVisible(true)
      return
    }
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          mounted.current = true
          setVisible(true)
          io.disconnect()
        }
      },
      { rootMargin: '300px 0px' },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [visible, id])

  return (
    <div ref={ref} id={id} style={{ minHeight: visible ? undefined : minHeight }}>
      {visible ? children : null}
    </div>
  )
}
