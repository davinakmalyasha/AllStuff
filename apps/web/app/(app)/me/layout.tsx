"use client"

import { RequireAuth } from '@/components/guards'
import { UserShell } from '@/app/shells/UserShell'

export default function MeLayout({ children }: { children: React.ReactNode }) {
  return <RequireAuth>
      <UserShell>{children}</UserShell>
    </RequireAuth>
}
