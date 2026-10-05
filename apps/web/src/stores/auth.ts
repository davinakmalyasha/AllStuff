import { create } from 'zustand'
import { useShallow } from 'zustand/react/shallow'
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
  // Adopt a challenge that arrived out of band, which today means the OAuth
  // callback redirecting to /2fa#challenge=... . It is a setter rather than a
  // second submit path so there is one place that owns the pending challenge:
  // login() and this must not be able to disagree about what is outstanding.
  setTwoFaChallenge: (challenge: string | null) => void
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

  setTwoFaChallenge: (challenge) => set({ twoFaChallenge: challenge }),

  logout: async () => {
    try {
      await api('/auth/logout', { method: 'POST' })
    } catch {
      // Swallowed deliberately, and this is load-bearing.
      //
      // The only caller is `void logout().then(() => navigate('/login'))`. With a
      // bare try/finally a rejected POST ran the finally block - clearing the user
      // - and then REJECTED, so `.then` never ran. The result was the worst
      // possible half-state: the UI said signed out, the user was still sitting on
      // /me because nothing navigated, and the refresh cookie was still valid, so
      // the next refresh silently logged them back in.
      //
      // The local session is cleared in `finally` either way, so there is nothing
      // for the caller to do about a failure here. Swallowing it means the promise
      // always resolves and the caller always navigates.
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

/**
 * Reads several auth fields at once WITHOUT subscribing to the whole store.
 *
 * WHY THIS EXISTS
 * ---------------
 * `useAuth()` with no selector subscribes the component to the entire state
 * object, so every `set()` re-renders it. `fetchMe` alone calls `set()` three
 * times (loading, user, then loading+initialized), which means three full-app
 * re-renders on every page load, and again on every login and logout. With 35
 * call sites — the header, the shells, and every page — that is a lot of work
 * triggered by state the component does not read.
 *
 * `useShallow` compares the SELECTED fields, so a component re-renders only
 * when one of the fields it actually destructures changes identity. The
 * alternative is rewriting all 35 sites to individual selectors, which fixes
 * today's call sites and silently regresses the next one someone adds — so the
 * safe default lives here instead.
 *
 * For a single field, prefer the selector form directly (`useAuth(s => s.user)`),
 * which needs no comparison at all.
 *
 * Note the actions (login, logout, fetchMe, …) are stable references: the store
 * is created once, so they never change identity and cannot cause a re-render.
 */
export function useAuthState<T>(selector: (s: AuthState) => T): T {
  return useAuth(useShallow(selector))
}
