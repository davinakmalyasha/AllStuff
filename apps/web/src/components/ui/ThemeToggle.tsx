import { Moon, Sun } from 'lucide-react'
import { useTheme } from '@/theme/ThemeProvider'

export function ThemeToggle({ className = '' }: { className?: string }) {
  const { theme, toggle } = useTheme()
  return (
    <button
      type="button"
      onClick={toggle}
      aria-label={theme === 'light' ? 'Switch to dark mode' : 'Switch to light mode'}
      title={theme === 'light' ? 'Dark mode' : 'Light mode'}
      className={`inline-flex h-9 w-9 items-center justify-center rounded-lg border border-border text-ink2 transition-colors hover:bg-surface2 hover:text-ink ${className}`}
    >
      {theme === 'light' ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4" />}
    </button>
  )
}
