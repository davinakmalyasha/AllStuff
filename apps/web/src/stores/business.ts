import { create } from 'zustand'

interface BusinessState {
  selectedId: string | null
  setSelected: (id: string | null) => void
}

export const useBusiness = create<BusinessState>((set) => ({
  selectedId: (() => {
    try {
      return localStorage.getItem('bv.biz')
    } catch {
      return null
    }
  })(),
  setSelected: (id) => {
    try {
      if (id) localStorage.setItem('bv.biz', id)
      else localStorage.removeItem('bv.biz')
    } catch {
      /* noop */
    }
    set({ selectedId: id })
  },
}))
