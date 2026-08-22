import { useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Logo } from '@/components/ui/Logo'
import { ThemeToggle } from '@/components/ui/ThemeToggle'

export function AuthShell({
  title,
  subtitle,
  children,
  footer,
}: {
  title: string
  subtitle: string
  children: ReactNode
  footer?: ReactNode
}) {
  return (
    <div className="flex min-h-screen flex-col bg-bg">
      <header className="flex h-16 items-center justify-between border-b border-border px-4 sm:px-8">
        <Logo />
        <ThemeToggle />
      </header>
      <main className="flex flex-1 items-center justify-center px-4 py-12">
        <div className="w-full max-w-sm">
          <h1 className="text-2xl font-semibold tracking-tight text-ink">{title}</h1>
          <p className="mt-1.5 text-sm text-ink2">{subtitle}</p>
          <div className="mt-8">{children}</div>
          {footer && <div className="mt-6 text-center text-sm text-ink2">{footer}</div>}
        </div>
      </main>
    </div>
  )
}

export function AuthFooterLink({
  text,
  to,
  label,
}: {
  text: string
  to: string
  label: string
}) {
  return (
    <p className="text-ink2">
      {text}{' '}
      <Link to={to} className="font-medium text-ink underline underline-offset-4 hover:text-ink2">
        {label}
      </Link>
    </p>
  )
}

export function useForm<T extends Record<string, string>>(initial: T) {
  const [values, setValues] = useState<T>(initial)
  const set = (key: keyof T) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setValues((v) => ({ ...v, [key]: e.target.value }))
  return { values, set, setValues }
}
