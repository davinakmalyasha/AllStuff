import { create } from 'zustand'
import { api, type UserDTO } from '@/lib/api'
import { queryClient } from '@/lib/queryClient'
import { ws } from '@/lib/ws'
import { resetCompareSession } from '@/stores/compare'

interface AuthState {
  user: UserDTO | null
  loading: boolean
  initialized: boolean
  // 2FA challenge issued by /auth/login when TOTP is enrolled; null unless
  // an uncompleted challenge is pending (PRD §5.9.1).
  twoFaChallenge: string | null
  register: (input: {
    email: string
    password: string
    name: string
    username: string
  }) => Promise<void>
  login: (email: string, password: string) => Promise<void>
  verify2FA: (challenge: string, code: string) => Promise<void>
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
  twoFaChallenge: null,

  register: async (input) => {
    const user = await api<UserDTO>('/auth/register', { method: 'POST', body: input })
    set({ user })
  },

  login: async (email, password) => {
    type TwoFaResponse = { '2fa_required': true; challenge: string; user: UserDTO }
    const res = await api<UserDTO | TwoFaResponse>('/auth/login', {
      method: 'POST',
      body: { email, password },
    })
    if ('challenge' in res) {
      // 2FA gate: no session cookies exist yet; stash the challenge so the
      // login page can collect a TOTP/recovery code.
      set({ twoFaChallenge: res.challenge })
      return
    }
    set({ user: res, twoFaChallenge: null })
  },

  verify2FA: async (challenge, code) => {
    const user = await api<UserDTO>('/auth/2fa/verify', {
      method: 'POST',
      body: { challenge, code },
    })
    set({ user, twoFaChallenge: null })
  },

  logout: async () => {
    try {
      await api('/auth/logout', { method: 'POST' })
    } finally {
      set({ user: null, twoFaChallenge: null })
      // Cross-account bleed: without this the next user on a shared machine
      // saw the previous user's threads/notifications flash from cache, and
      // a zombie WebSocket kept reconnecting forever.
      queryClient.clear()
      ws.close()
      resetCompareSession()
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
