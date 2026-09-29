import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './index.css'

/**
 * Phase 5 — server-rendered SEO shell.
 *
 * For the SEO-critical routes (/b, /c, /city) the API serves a complete HTML
 * document whose crawlable content lives in a sibling `#seo` node, while
 * `#root` is left empty for the SPA. A crawler that never executes JS reads
 * `#seo`; a browser mounts the app into `#root` and must then remove `#seo`,
 * or the visitor sees the same content twice.
 *
 * Removal happens BEFORE the first render rather than in an effect, so there is
 * no frame in which both are visible.
 */
const rootEl = document.getElementById('root')
if (rootEl) {
  document.getElementById('seo')?.remove()
  createRoot(rootEl).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
}
