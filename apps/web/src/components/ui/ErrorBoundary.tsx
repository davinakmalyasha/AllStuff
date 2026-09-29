import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  children: ReactNode
  /** Shown in the fallback; defaults to something route-agnostic. */
  label?: string
  /** Reported to the error sink alongside the message. */
  context?: Record<string, string>
}

interface State {
  error: Error | null
  info: string
}

/**
 * Route-level error boundary.
 *
 * WHY THIS EXISTS
 * ---------------
 * There was no ErrorBoundary anywhere in the app. A render throw therefore
 * unmounted the entire React tree and produced a blank white page with only a
 * `window.onerror` POST behind it, requiring a hard refresh. Two pages did throw
 * on a routine failure: `AdminKPIPage` dereferenced `data as KPIDTO` without
 * checking `isError`, so a single failed fetch after `retry: 1` was a total
 * white-out of the whole app rather than one failed panel.
 *
 * Wrapping each lazy route means a single broken page degrades to a retryable
 * panel while the rest of the shell (including the notification bell and the
 * logout control) keeps working — which matters most on the admin routes, where
 * a white screen also removes the only way to sign out.
 *
 * Reporting is best-effort: a failed report must never replace the fallback UI.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, info: '' }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    const { label, context } = this.props
     
    console.error('render error', label, error)
    try {
      void fetch('/api/v1/errors', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        keepalive: true,
        body: JSON.stringify({
          message: error.message,
          route: label ?? window.location.pathname,
          ...context,
          componentStack: info.componentStack?.slice(0, 4000),
        }),
      }).catch(() => {
        // A failed report is not the user's problem; swallow it.
      })
    } catch {
      // fetch can throw synchronously on a malformed URL. Never let reporting
      // replace the error UI.
    }
  }

  retry = () => {
    this.setState({ error: null, info: '' })
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children

    return (
      <div
        role="alert"
        className="mx-auto flex min-h-[40vh] max-w-lg flex-col items-center justify-center gap-3 px-4 text-center"
      >
        <p className="font-mono text-3xl font-semibold tracking-tight">Something broke</p>
        <p className="text-sm text-ink2">
          This section failed to render. The rest of the page is still usable.
        </p>
        {/* The message is developer-facing detail; it is shown but the layout
            does not depend on its contents, so an unusually long or
            multi-line message cannot break the layout. */}
        <p className="max-w-full break-words text-xs text-ink3">{error.message}</p>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={this.retry}
            className="rounded-lg border border-border px-3 py-1.5 text-sm text-ink hover:bg-surface2"
          >
            Try again
          </button>
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="rounded-lg px-3 py-1.5 text-sm text-ink2 hover:bg-surface2"
          >
            Reload page
          </button>
        </div>
      </div>
    )
  }
}
