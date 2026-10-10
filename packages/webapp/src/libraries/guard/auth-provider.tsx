import { useNavigate, useRouter } from '@tanstack/react-router'
import { useCallback, useEffect, useMemo } from 'react'
import { AuthContext, useAuth, type AuthLoginContextOptions } from '#/hooks/use-auth'
import type { CompleteSignInFactor, SignInOutcome } from '#/libraries/guard/auth-engine'
import { queryClient } from '../api-client'
import type { LoginCredentials } from './auth-engine'
import { ensureSessionLoaded, refreshIfExpiring } from './auth-session'
import { clearAuth, setAuthUser, setSigningOut } from './auth-store'
import { safeReturnTo } from './auth-utils'
import { authWorker } from './auth-worker-client'

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
  // persists the pair to the cookie via the worker client. A multi-factor
  // fork returns its outcome to the view — the fork owns the navigation.
  const handleLogin = useCallback(
    async (
      credentials: LoginCredentials,
      options?: AuthLoginContextOptions
    ): Promise<SignInOutcome> => {
      const { redirectTo, ...workerOptions } = options ?? {}
      const outcome = await authWorker().login(credentials, workerOptions)
      if (outcome.kind !== 'signed-in') return outcome
      setAuthUser(outcome.session.user)
      // The cache may hold the previous account's data — a re-login as a
      // different account must never render it.
      queryClient.clear()
      const target = safeReturnTo(redirectTo)
      if (target) {
        router.history.push(target)
      } else {
        void navigate({ to: '/overview' })
      }
      return outcome
    },
    [navigate, router]
  )

  const handleContinueSignIn = useCallback(
    async (flowToken: string): Promise<SignInOutcome> => {
      const outcome = await authWorker().continueSignIn(flowToken)
      if (outcome.kind !== 'signed-in') return outcome
      setAuthUser(outcome.session.user)
      // The flow's redirect chain never carried the cache's account — clear it
      // the same way a password sign-in does.
      queryClient.clear()
      void navigate({ to: '/overview' })
      return outcome
    },
    [navigate]
  )

  const handleCompleteSignIn = useCallback(
    async (
      pendingToken: string,
      factor: CompleteSignInFactor,
      options?: AuthLoginContextOptions
    ) => {
      const profile = await authWorker().completeSignIn(pendingToken, factor)
      setAuthUser(profile)
      // The bridge's account was never the cache's — clear it the same way a
      // password sign-in does.
      queryClient.clear()
      const target = safeReturnTo(options?.redirectTo)
      if (target) {
        router.history.push(target)
      } else {
        void navigate({ to: '/overview' })
      }
    },
    [navigate, router]
  )

  const handleLogout = useCallback(() => {
    // The eviction effect in the `(app)` layout watches this same profile —
    // mark the sign-out first so its redirect never races the goodbye one.
    setSigningOut(true)
    void authWorker()
      .logout()
      .finally(() => {
        // The query cache holds the previous account's data — a re-login as
        // a different account must never render it.
        queryClient.clear()
        clearAuth()
        // The page the user is leaving is where a re-login should land.
        void navigate({
          to: '/login',
          search: { loggedOut: true, return_to: router.state.location.href }
        }).finally(() => setSigningOut(false))
      })
  }, [navigate, router])

  const context = useMemo(
    () => ({
      user,
      loggedIn,
      isLoading,
      login: handleLogin,
      continueSignIn: handleContinueSignIn,
      completeSignIn: handleCompleteSignIn,
      logout: handleLogout
    }),
    [
      user,
      loggedIn,
      isLoading,
      handleLogin,
      handleContinueSignIn,
      handleCompleteSignIn,
      handleLogout
    ]
  )

  return <AuthContext.Provider value={context}>{children}</AuthContext.Provider>
}
