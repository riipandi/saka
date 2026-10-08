import { useNavigate, useRouter } from '@tanstack/react-router'
import { useSelector } from '@tanstack/react-store'
import { createContext, useCallback, useContext, useEffect, useMemo } from 'react'
import type { LoginCredentials } from '#/schemas/auth.schema'
import { queryClient } from '../api-client'
import type { AuthLoginOptions } from './auth-engine'
import { ensureSessionLoaded, refreshIfExpiring } from './auth-session'
import { authStore, clearAuth, setAuthUser, type AuthState, type UserProfile } from './auth-store'
import { safeReturnTo } from './auth-utils'
import { authWorker } from './auth-worker-client'

/** Subscribe to the session state (selector-based, minimal re-renders). */
export function useAuth(): AuthState {
  return useSelector(authStore, (state) => state)
}

/**
 * Subscribe to just the user profile — components that only render user
 * data skip re-renders triggered by `isLoading` flips.
 */
export function useAuthUser(): UserProfile | null {
  return useSelector(authStore, (state) => state.user)
}

/** Worker login options plus the post-login redirect target. */
interface AuthLoginContextOptions extends AuthLoginOptions {
  /** Path (with optional query) captured by the auth guard — see `(app)/route.tsx`. */
  redirectTo?: string
}

interface AuthContext {
  user: UserProfile | null
  loggedIn: boolean
  isLoading: boolean
  login: (credentials: LoginCredentials, options?: AuthLoginContextOptions) => Promise<void>
  logout: () => void
}

const DefaultAuthContext: AuthContext = {
  user: null,
  loggedIn: false,
  isLoading: false,
  login: async () => {},
  logout: () => {}
}

const AuthContextReact = createContext(DefaultAuthContext)

/** Refresh when the session expires within this window after the tab refocuses. */
const REFRESH_ON_VISIBLE_WITHIN_MS = 5 * 60_000

export function AuthProvider({ children }: React.PropsWithChildren) {
  const navigate = useNavigate()
  const router = useRouter()
  const { user, isLoading } = useAuth()
  const loggedIn = user !== null

  // Kick off the silent session bootstrap. Route guards await the same
  // promise, so navigation never races the refresh.
  useEffect(() => {
    void ensureSessionLoaded()
  }, [])

  // The worker's proactive timer is throttled in background tabs,
  // refresh on tab focus when the session is about to expire.
  useEffect(() => {
    const onVisibilityChange = () => {
      if (document.visibilityState !== 'visible') return
      void refreshIfExpiring(REFRESH_ON_VISIBLE_WITHIN_MS)
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => document.removeEventListener('visibilitychange', onVisibilityChange)
  }, [])

  // The worker holds the token pair and answers the profile; this layer
  // persists the pair to the cookie via the worker client.
  const handleLogin = useCallback(
    async (credentials: LoginCredentials, options?: AuthLoginContextOptions) => {
      const { redirectTo, ...workerOptions } = options ?? {}
      const profile = await authWorker().login(credentials, workerOptions)
      setAuthUser(profile)
      const target = safeReturnTo(redirectTo)
      if (target) {
        router.history.push(target)
      } else {
        void navigate({ to: '/overview' })
      }
    },
    [navigate, router]
  )

  const handleLogout = useCallback(() => {
    void authWorker()
      .logout()
      .finally(() => {
        // The query cache holds the previous account's data — a re-login as
        // a different account must never render it.
        queryClient.clear()
        clearAuth()
        void navigate({ to: '/login', search: { loggedOut: true } })
      })
  }, [navigate])

  const context = useMemo(
    () => ({ user, loggedIn, isLoading, login: handleLogin, logout: handleLogout }),
    [user, loggedIn, isLoading, handleLogin, handleLogout]
  )

  return <AuthContextReact.Provider value={context}>{children}</AuthContextReact.Provider>
}

export function useAuthentication() {
  return useContext(AuthContextReact)
}
