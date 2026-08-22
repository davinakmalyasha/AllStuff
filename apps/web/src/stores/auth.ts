import { create } from 'zustand'
import { api, type UserDTO } from '@/lib/api'

interface AuthState {
  user: UserDTO | null
  loading: boolean
  initialized: boolean
  register: (input: {
    email: string
    password: string
    name: string
    username: string
  }) => Promise<void>
  login: (email: string, password: string) => Promise<void>
  logout: () => Promise<void>
  fetchMe: () => Promise<void>
  verifyEmail: (token: string) => Promise<void>
  forgotPassword: (email: string) => Promise<void>
  resetPassword: (token: string, password: string) => Promise<void>
}

export const useAuth = create<AuthState>((set) => ({
  user: null,
  loading: false,
  initialized: false,

  register: async (input) => {
    const user = await api<UserDTO>('/auth/register', { method: 'POST', body: input })
    set({ user })
  },

  login: async (email, password) => {
    const user = await api<UserDTO>('/auth/login', { method: 'POST', body: { email, password } })
    set({ user })
  },

  logout: async () => {
    try {
      await api('/auth/logout', { method: 'POST' })
    } finally {
      set({ user: null })
    }
  },

  fetchMe: async () => {
    set({ loading: true })
    try {
      const user = await api<UserDTO>('/me')
      set({ user })
    } catch {
      set({ user: null })
    } finally {
      set({ loading: false, initialized: true })
    }
  },

  verifyEmail: async (token) => {
    await api('/auth/verify-email', { method: 'POST', body: { token } })
  },

  forgotPassword: async (email) => {
    await api('/auth/forgot-password', { method: 'POST', body: { email } })
  },

  resetPassword: async (token, password) => {
    await api('/auth/reset-password', { method: 'POST', body: { token, password } })
  },
}))
