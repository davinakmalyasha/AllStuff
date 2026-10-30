"use client"

import { RequireAuth } from '@/components/guards'
import { OwnerShell } from '@/app/shells/OwnerShell'

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  return <RequireAuth>
      <OwnerShell>{children}</OwnerShell>
    </RequireAuth>
}
