/** Google sign-in button (PRD §5.9.1). */
export function GoogleButton({ label = 'Continue with Google' }: { label?: string }) {
  return (
    <a
      href="/api/v1/auth/oauth/google"
      className="flex h-10 w-full items-center justify-center gap-2 rounded-lg border border-border text-sm font-medium text-ink transition-colors hover:bg-surface2"
    >
      <svg viewBox="0 0 24 24" className="h-4 w-4" aria-hidden>
        <path fill="#4285F4" d="M23.5 12.27c0-.85-.08-1.66-.22-2.45H12v4.64h6.45a5.52 5.52 0 0 1-2.39 3.62v3h3.87c2.26-2.09 3.57-5.17 3.57-8.81z" />
        <path fill="#34A853" d="M12 24c3.24 0 5.96-1.07 7.93-2.91l-3.87-3c-1.07.72-2.44 1.15-4.06 1.15-3.12 0-5.77-2.11-6.71-4.94H1.29v3.1A11.98 11.98 0 0 0 12 24z" />
        <path fill="#FBBC05" d="M5.29 14.3a7.2 7.2 0 0 1 0-4.6v-3.1H1.29a12 12 0 0 0 0 10.8l4-3.1z" />
        <path fill="#EA4335" d="M12 4.76c1.76 0 3.34.6 4.58 1.79l3.44-3.44A11.98 11.98 0 0 0 12 0 11.98 11.98 0 0 0 1.29 6.6l4 3.1C6.23 6.87 8.88 4.76 12 4.76z" />
      </svg>
      {label}
    </a>
  )
}
