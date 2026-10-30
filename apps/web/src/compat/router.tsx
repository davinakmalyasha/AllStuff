"use client"

// Compatibility shim: lets the existing Vite-era components (which import
// from 'react-router-dom') compile and behave identically under Next.js App
// Router without editing all 46 call sites. New Next code must import from
// 'next/link' / 'next/navigation' directly; this shim is migration scaffolding
// for the copied feature tree only.

import LinkPrimitive from 'next/link'
import {
  useParams as useNextParams,
  usePathname,
  useRouter,
  useSearchParams as useNextSearchParams,
} from 'next/navigation'
import { useCallback, useEffect, useMemo, type ReactNode } from 'react'

type To = string | { pathname?: string; search?: string; hash?: string }

function resolveTo(to: To): string {
  if (typeof to === 'string') return to
  return `${to.pathname ?? ''}${to.search ?? ''}${to.hash ?? ''}`
}

// react-router location.state replacement: businessWizard -> verification page
// passes { justSubmitted: true }. Same-tab in-memory store keyed by pathname.
const navState = new Map<string, unknown>()

export function Link({
  to,
  href,
  replace,
  children,
  ...rest
}: {
  to?: To
  href?: string
  replace?: boolean
  children?: ReactNode
  [key: string]: unknown
}) {
  const target = href ?? (to !== undefined ? resolveTo(to) : '/')
  return (
    <LinkPrimitive href={target} replace={replace} {...(rest as Record<string, unknown>)}>
      {children as ReactNode}
    </LinkPrimitive>
  )
}

export function NavLink({
  to,
  end,
  className,
  style,
  children,
  ...rest
}: {
  to: To
  end?: boolean
  className?: string | ((props: { isActive: boolean }) => string)
  style?: React.CSSProperties | ((props: { isActive: boolean }) => React.CSSProperties)
  children?: ReactNode | ((props: { isActive: boolean }) => ReactNode)
  [key: string]: unknown
}) {
  const pathname = usePathname()
  const target = resolveTo(to)
  const targetPath = target.split('?')[0].split('#')[0]
  const isActive = end ? pathname === targetPath : pathname === targetPath || pathname.startsWith(targetPath + '/')
  const cls = typeof className === 'function' ? className({ isActive }) : className
  const sty = typeof style === 'function' ? style({ isActive }) : style
  const content = typeof children === 'function' ? children({ isActive }) : children
  return (
    <LinkPrimitive
      href={target}
      className={cls as string | undefined}
      style={sty as React.CSSProperties | undefined}
      aria-current={isActive ? 'page' : undefined}
      {...(rest as Record<string, unknown>)}
    >
      {content as ReactNode}
    </LinkPrimitive>
  )
}

/** In Next layouts nesting is via `children`, not <Outlet/>. Shells are
 * rewritten as layouts that pass children; this exists only so untouched
 * files still compile. */
export function Outlet() {
  return null
}

export function Navigate({ to, replace }: { to: To; replace?: boolean }) {
  const router = useRouter()
  const target = resolveTo(to)
  useEffect(() => {
    if (replace) router.replace(target)
    else router.push(target)
  }, [router, target, replace])
  return null
}

export function useNavigate() {
  const router = useRouter()
  return (to: To | number, opts?: { replace?: boolean; state?: unknown }) => {
    if (typeof to === 'number') {
      if (to === -1) router.back()
      else if (to === 1) router.forward()
      return
    }
    const target = resolveTo(to)
    if (opts?.state !== undefined) navState.set(target.split('?')[0].split('#')[0], opts.state)
    if (opts?.replace) router.replace(target)
    else router.push(target)
  }
}

export function useLocation() {
  const pathname = usePathname()
  const raw = useNextSearchParams()
  const search = raw?.toString()
  return {
    pathname,
    search: search ? `?${search}` : '',
    hash: '',
    state: (navState.get(pathname) ?? null) as unknown,
  }
}

export function useParams<T extends Record<string, string> = Record<string, string>>(): T {
  const p = useNextParams() as Record<string, string | string[]>
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(p ?? {})) out[k] = Array.isArray(v) ? (v[0] ?? '') : (v as string)
  return out as T
}

/**
 * react-router tuple form: [params, setParams]. Backed by Next navigation;
 * setParams accepts a URLSearchParams or an updater, plus { replace }.
 * Every call site already renders inside a Suspense boundary (page wrappers),
 * satisfying Next's useSearchParams requirement.
 */
export function useSearchParams(): [
  URLSearchParams,
  (
    next: URLSearchParams | ((prev: URLSearchParams) => URLSearchParams),
    opts?: { replace?: boolean },
  ) => void,
] {
  const raw = useNextSearchParams()
  const rawString = raw?.toString() ?? ''
  const pathname = usePathname()
  const router = useRouter()
  const params = useMemo(() => new URLSearchParams(rawString), [rawString])
  const set = useCallback(
    (
      next: URLSearchParams | ((prev: URLSearchParams) => URLSearchParams),
      opts?: { replace?: boolean },
    ) => {
      const prev = new URLSearchParams(rawString)
      const resolved = typeof next === 'function' ? next(prev) : next
      const qs = resolved.toString()
      const url = qs ? `${pathname}?${qs}` : pathname
      if (opts?.replace) router.replace(url)
      else router.push(url)
    },
    [rawString, pathname, router],
  )
  return [params, set]
}
