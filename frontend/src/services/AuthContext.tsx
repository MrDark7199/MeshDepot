import { createContext, useContext, createSignal, JSX } from 'solid-js'
import { api } from './api'
import { useI18n } from '../i18n/index'
import { applyCustomCss } from '../utils/customCss'
import type { User } from '../types'

interface AuthCtx {
  user: () => User | null
  /** Replaces the current user everywhere it is kept - signal and cache alike.
   *  Views that change the profile have to go through this: a copy updated only
   *  in a local signal is gone on the next reload, which is how a completed
   *  password change kept asking for one. */
  updateUser: (user: User) => void
  login: (email: string, password: string, remember: boolean) => Promise<{ totp_required?: boolean; pending_token?: string }>
  totpVerify: (pendingToken: string, code: string, remember: boolean) => Promise<void>
  logout: () => Promise<void>
  loading: () => boolean
}

const AuthContext = createContext<AuthCtx>({
  user: () => null,
  updateUser: () => {},
  login: async () => ({}),
  totpVerify: async () => {},
  logout: async () => {},
  loading: () => false,
})

export function AuthProvider(props: { children: JSX.Element }) {
  const [user, setUser] = createSignal<User | null>(null)
  const [loading, setLoading] = createSignal(true)
  const { setLang, availableLangs } = useI18n()

  /**
   * Switches the UI to the language stored on the account.
   *
   * The preference used to live in this browser's localStorage alone, so the
   * same account answered in a different language on the next device, and a
   * fresh one - the seeded admin, whose column says 'en' - started in whatever
   * the last visitor of that browser had picked.
   */
  const applyLanguage = (next: User | null) => {
    const language = next?.language
    if (language && availableLangs.includes(language)) setLang(language)
  }

  /** Writes the user to the signal and back into whichever storage holds it. */
  const updateUser = (next: User) => {
    setUser(next)
    applyLanguage(next)
    applyCustomCss(next.custom_css || '')
    const storage = localStorage.getItem('meshdepot_user') ? localStorage
      : sessionStorage.getItem('meshdepot_user') ? sessionStorage : null
    storage?.setItem('meshdepot_user', JSON.stringify(next))
  }

  const stored = localStorage.getItem('meshdepot_user') || sessionStorage.getItem('meshdepot_user')
  if (stored) {
    try {
      const cached = JSON.parse(stored)
      setUser(cached)
      applyLanguage(cached)
      // From the cache first so the styling is there before /auth/me answers;
      // the revalidation below replaces it with the stored one.
      applyCustomCss(cached.custom_css || '')
    } catch {}
  }
  setLoading(false)

  // The cached copy above is only for instant render. Revalidate against the
  // server so changes made in a previous session (avatar, name, language, admin
  // flag, …) survive a hard refresh instead of reverting to the stale cache.
  // A genuine 401 triggers the app's existing auth:unauthorized logout handler.
  if (stored) {
    api.me()
      .then((res: { data: User }) => { if (res?.data) updateUser(res.data) })
      .catch(() => {}) // network hiccup: keep the cached user; 401s are handled elsewhere
  }

  const login = async (email: string, password: string, remember: boolean) => {
    const res = await api.login({ email, password, remember }) as { data: User & { totp_required?: boolean; pending_token?: string } }
    if (res.data?.totp_required) {
      return { totp_required: true, pending_token: res.data.pending_token }
    }
    setUser(res.data)
    applyLanguage(res.data)
    applyCustomCss(res.data.custom_css || '')
    const storage = remember ? localStorage : sessionStorage
    storage.setItem('meshdepot_user', JSON.stringify(res.data))
    return {}
  }

  const totpVerify = async (pendingToken: string, code: string, remember: boolean) => {
    const res = await api.totpVerify({ pending_token: pendingToken, code, remember }) as { data: User }
    setUser(res.data)
    applyLanguage(res.data)
    applyCustomCss(res.data.custom_css || '')
    const storage = remember ? localStorage : sessionStorage
    storage.setItem('meshdepot_user', JSON.stringify(res.data))
  }

  const logout = async () => {
    try { await api.logout() } catch {}
    setUser(null)
    // The next account gets its own look, or none.
    applyCustomCss('')
    localStorage.removeItem('meshdepot_user')
    sessionStorage.removeItem('meshdepot_user')
  }

  return (
    <AuthContext.Provider value={{ user, updateUser, login, totpVerify, logout, loading }}>
      {props.children}
    </AuthContext.Provider>
  )
}

export const useAuth = () => useContext(AuthContext)
