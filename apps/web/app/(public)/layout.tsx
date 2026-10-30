"use client"

import { PublicLayout } from '@/components/layout/PublicLayout'

export default function PublicGroupLayout({ children }: { children: React.ReactNode }) {
  return <PublicLayout>{children}</PublicLayout>
}
