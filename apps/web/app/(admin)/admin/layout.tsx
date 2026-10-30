"use client"

import { RequireAdmin, RequireAuth } from '@/components/guards'
import { AdminShell } from '@/app/shells/AdminShell'

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  return <RequireAuth>
      <RequireAdmin>
        <AdminShell>{children}</AdminShell>
      </RequireAdmin>
    </RequireAuth>
}
