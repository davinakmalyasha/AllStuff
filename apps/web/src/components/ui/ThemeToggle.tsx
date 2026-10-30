"use client"

import { Moon, Sun } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTheme } from '@/theme/ThemeProvider'

export function ThemeToggle({ className = '' }: { className?: string }) {
  const { theme, toggle } = useTheme()
  // Mount gate: the server always prerenders the light icon, but a client
  // with a saved dark theme would render the sun on first paint — a
  // hydration mismatch on every dark-mode load. Render a size-identical
  // placeholder until mounted so both passes agree, then swap to the real
  // icon with no layout shift (same h-9 w-9 box, same h-4 w-4 glyph slot).
  const [mounted, setMounted] = useState(false)
  useEffect(() => setMounted(true), [])
  return (
    <button
      type="button"
      onClick={toggle}
      aria-label={mounted && theme !== 'light' ? 'Switch to light mode' : 'Switch to dark mode'}
      title={mounted && theme !== 'light' ? 'Light mode' : 'Dark mode'}
      className={`inline-flex h-9 w-9 items-center justify-center rounded-lg border border-border text-ink2 transition-colors hover:bg-surface2 hover:text-ink ${className}`}
    >
      {!mounted || theme === 'light' ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4" />}
    </button>
  )
}
