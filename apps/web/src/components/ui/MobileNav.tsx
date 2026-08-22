import { useState } from 'react'
import { NavLink } from 'react-router-dom'
import { Menu, X } from 'lucide-react'

/** Mobile fallback for dashboard/admin sidebars (hamburger). */
export function MobileNav({ links }: { links: { to: string; label: string; end?: boolean }[] }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="md:hidden">
      <button onClick={() => setOpen((v) => !v)} className="rounded-lg border border-border p-2 text-ink2" aria-label="Menu">
        {open ? <X className="h-4 w-4" /> : <Menu className="h-4 w-4" />}
      </button>
      {open && (
        <div className="absolute left-4 right-4 top-16 z-50 rounded-xl border border-border bg-surface p-2 shadow-cardHover">
          {links.map((l) => (
            <NavLink
              key={l.to}
              to={l.to}
              end={l.end}
              onClick={() => setOpen(false)}
              className={({ isActive }) =>
                `block rounded-lg px-3 py-2.5 text-sm ${isActive ? 'bg-accent text-accent-ink' : 'text-ink2 hover:bg-surface2'}`
              }
            >
              {l.label}
            </NavLink>
          ))}
        </div>
      )}
    </div>
  )
}
