import { useSelector } from '@tanstack/react-store'
import { createContext, useContext } from 'react'
import type { AuthLoginOptions, LoginCredentials } from '#/libraries/guard/auth-engine'
import { authStore, type AuthState, type UserProfile } from '#/libraries/guard/auth-store'

/** Subscribe to the whole session state. */
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
export interface AuthLoginContextOptions extends AuthLoginOptions {
  /** Path (with optional query) captured by the auth guard — see `(app)/route.tsx`. */
  redirectTo?: string
}

export interface AuthContextValue {
  user: UserProfile | null
  loggedIn: boolean
  isLoading: boolean
  login: (credentials: LoginCredentials, options?: AuthLoginContextOptions) => Promise<void>
  /** Complete an OAuth SSO flow the callback route redirected with. */
  continueSignIn: (flowToken: string) => Promise<void>
  logout: () => void
}

export const AuthContext = createContext<AuthContextValue>({
  user: null,
  loggedIn: false,
  isLoading: false,
  login: async () => {},
  continueSignIn: async () => {},
  logout: () => {}
})

export function useAuthentication() {
  return useContext(AuthContext)
}
