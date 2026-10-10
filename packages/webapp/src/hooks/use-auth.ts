import { useSelector } from '@tanstack/react-store'
import { createContext, useContext } from 'react'
import type {
  AuthLoginOptions,
  CompleteSignInFactor,
  LoginCredentials,
  SignInOutcome
} from '#/libraries/guard/auth-engine'
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
  /**
   * Sign in. Resolves the sign-in's outcome — a signed-in answer has already
   * navigated; a fork (`mfa-challenge`, `enrollment-required`) is the view's
   * to finish, in its own state machine, without a URL.
   */
  login: (
    credentials: LoginCredentials,
    options?: AuthLoginContextOptions
  ) => Promise<SignInOutcome>
  /** Complete an OAuth SSO flow the callback route redirected with. Same outcome shape. */
  continueSignIn: (flowToken: string) => Promise<SignInOutcome>
  /**
   * Spend the pending bridge the sign-in forked with. Resolves after the
   * session was established and the navigation already happened — the view's
   * machine hands the bridge over and is done.
   */
  completeSignIn: (
    pendingToken: string,
    factor: CompleteSignInFactor,
    options?: AuthLoginContextOptions
  ) => Promise<void>
  /**
   * Finish a discoverable passkey sign-in the login entry began. Resolves
   * after the session was established and the navigation already happened.
   */
  verifyPasskeyLogin: (
    sessionId: string,
    credential: string,
    options?: AuthLoginContextOptions
  ) => Promise<void>
  logout: () => void
}

export const AuthContext = createContext<AuthContextValue>({
  user: null,
  loggedIn: false,
  isLoading: false,
  login: async () => {
    throw new Error('AuthProvider is not mounted.')
  },
  continueSignIn: async () => {
    throw new Error('AuthProvider is not mounted.')
  },
  completeSignIn: async () => {
    throw new Error('AuthProvider is not mounted.')
  },
  verifyPasskeyLogin: async () => {
    throw new Error('AuthProvider is not mounted.')
  },
  logout: () => {}
})

export function useAuthentication() {
  return useContext(AuthContext)
}
