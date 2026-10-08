import { create } from '@bufbuild/protobuf'
import { Code, ConnectError, createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { RPC_BASE_URL } from '#/libraries/api-client'
import type { LoginCredentials } from '#/schemas/auth.schema'
import {
  AuthService,
  GetSessionRequestSchema,
  RefreshRequestSchema,
  SessionService,
  SignInRequestSchema,
  SignOutRequestSchema
} from '~/codegen/authn_pb'
import type {
  AuthenticatedUser,
  GetSessionResponse,
  RefreshResponse,
  SignInResponse
} from '~/codegen/authn_pb'
import type { UserProfile } from './auth-store'

/** Refresh this long before the access token expires. */
const PROACTIVE_MARGIN_MS = 60_000

/** Skip repeated refresh attempts within this window after a failure. */
const REFRESH_COOLDOWN_MS = 5_000

/** setTimeout ceiling — delays above 2^31-1 ms overflow to ~0 in browsers. */
const MAX_TIMER_DELAY_MS = 2_147_483_647

/** The token pair the worker holds in memory and the main thread persists to cookies. */
export interface TokenBundle {
  accessToken: string
  /** Milliseconds since epoch — when the access token dies. */
  accessExpiresAt: number
  refreshToken: string
  /** Milliseconds since epoch — when the refresh token dies. */
  refreshExpiresAt: number
  /** The TypeID (`sess_…`) the refresh token is stored under server-side. */
  sessionId: string
}

/** What a successful sign-in hands back: the profile and the pair to persist. */
export interface AuthSession {
  user: UserProfile
  tokens: TokenBundle
}

/**
 * Reported on every token custody change — a fresh pair from sign-in or
 * rotation, or `null` when the pair is dropped (failed refresh, sign-out).
 * The main thread registers a listener (see `auth-worker-client.ts`) and this
 * is the *only* signal it needs: the cookie copy can never drift from the
 * worker's live pair, not even when the proactive timer rotates the pair
 * without any main-thread call in the loop.
 */
export type TokenListener = (tokens: TokenBundle | null) => void

/** Options for {@link AuthEngineApi.login}. */
export interface AuthLoginOptions {
  /** Ask the backend for the long-lived session window. */
  rememberMe?: boolean
}

/**
 * Transport-agnostic authn engine. Runs inside the Comlink worker (default) or
 * on the main thread as a fallback (SSR, tests, CSP-restricted environments).
 *
 * The worker is the only owner of the live token pair; it speaks the Connect
 * contract (`saka.authn.v1`) directly — SignIn issues the pair, Refresh
 * rotates it, SignOut spends the access token. Tokens never outlive the
 * worker's memory except through the cookie copy the main thread persists
 * (see `auth-cookies.ts` — workers have no cookie access) and handed back in
 * via {@link AuthEngineApi.restore}.
 */
export interface AuthEngineApi {
  /** Validate credentials and mint the token pair. Resolves the profile and tokens. */
  login(credentials: LoginCredentials, options?: AuthLoginOptions): Promise<AuthSession>
  /** Silent refresh — single-flight. Resolves `true` when a session is established. */
  refresh(): Promise<boolean>
  /** Refresh only when the access token expires within `withinMs`. Resolves `true` when still valid. */
  maybeRefresh(withinMs: number): Promise<boolean>
  /**
   * Hand a cookie-restored token pair to the engine at boot. The worker has no
   * cookie access, so the main thread reads the cookies and passes them in.
   */
  restore(tokens: TokenBundle | null): Promise<void>
  /**
   * Rebuild the profile from the session the access token names
   * (`SessionService/GetSession`), refreshing once first when the access
   * token has expired. Resolves `null` when no live session remains.
   */
  session(): Promise<UserProfile | null>
  /** The unexpired Bearer token for request interceptors, or null when absent. */
  accessToken(): Promise<string | null>
  /**
   * Register the custody-change listener (see {@link TokenListener}). The
   * main thread passes a `Comlink.proxy` callback; the worker engine calls it
   * across the boundary on every pair it takes or drops.
   */
  setTokenListener(listener: TokenListener | null): Promise<void>
  /** Terminate the session server-side and drop the in-memory pair. */
  logout(): Promise<void>
}

function toProfile(user: AuthenticatedUser): UserProfile {
  return {
    id: user.id,
    username: user.username,
    email: user.email,
    displayName: user.displayName
  }
}

/** The GetSession answer's account, mapped — the field is presence-based. */
function readProfile(response: GetSessionResponse): UserProfile | null {
  return response.user ? toProfile(response.user) : null
}

export function createAuthEngine(baseUrl: string = RPC_BASE_URL): AuthEngineApi {
  const transport = createConnectTransport({ baseUrl })
  const auth = createClient(AuthService, transport)
  const session = createClient(SessionService, transport)

  let tokens: TokenBundle | null = null
  let refreshInFlight: Promise<boolean> | null = null
  let lastFailedRefreshAt = 0
  let refreshTimer: ReturnType<typeof setTimeout> | null = null
  /** Session generation — bumped by login/logout to discard stale in-flight refreshes. */
  let sessionEpoch = 0
  /** The custody-change listener, set once by the main-thread wrapper. */
  let tokenListener: TokenListener | null = null

  function notify(next: TokenBundle | null) {
    tokenListener?.(next)
  }

  /** The GetSession call the profile rebuild is made of, with the pair's Bearer. */
  function readSession(bearer: string) {
    return session.getSession(create(GetSessionRequestSchema), {
      headers: { authorization: `Bearer ${bearer}` }
    })
  }

  function clearTimer() {
    if (refreshTimer) {
      clearTimeout(refreshTimer)
      refreshTimer = null
    }
  }

  function scheduleProactiveRefresh() {
    clearTimer()
    if (!tokens) return
    // Long-lived sessions overflow the 32-bit setTimeout ceiling — browsers
    // wrap delays above 2^31-1 ms to ~0, firing the refresh at once. Clamp
    // instead; the timer simply re-fires and reschedules.
    const rawDelay = tokens.accessExpiresAt - PROACTIVE_MARGIN_MS - Date.now()
    const delay = Math.min(Math.max(rawDelay, 0), MAX_TIMER_DELAY_MS)
    refreshTimer = setTimeout(() => {
      refreshTimer = null
      void api.refresh()
    }, delay)
  }
  /** Take custody of a fresh pair from SignIn or Refresh and return it. */
  function takeTokenPair(response: SignInResponse | RefreshResponse): TokenBundle {
    const next: TokenBundle = {
      accessToken: response.accessToken,
      accessExpiresAt: Date.now() + response.accessExpiresIn * 1000,
      refreshToken: response.refreshToken,
      refreshExpiresAt: Date.now() + response.refreshExpiresIn * 1000,
      sessionId: response.sessionId
    }
    tokens = next
    scheduleProactiveRefresh()
    notify(next)
    return next
  }

  async function doRefresh(): Promise<boolean> {
    if (!tokens?.refreshToken) return false
    // Snapshot the generation: if logout (or a fresh login) happens while the
    // request is in flight, its result must not resurrect the old session.
    const epoch = sessionEpoch
    try {
      const response = await session.refresh(
        create(RefreshRequestSchema, { refreshToken: tokens.refreshToken })
      )
      if (epoch !== sessionEpoch) return false
      takeTokenPair(response)
      return true
    } catch {
      if (epoch !== sessionEpoch) return false
      // A failed refresh means the pair is spent or refused — drop it so a
      // later attempt does not replay a dead token. The listener clears the
      // cookie copy in the same breath.
      tokens = null
      lastFailedRefreshAt = Date.now()
      clearTimer()
      notify(null)
      return false
    }
  }

  const api: AuthEngineApi = {
    async login(credentials, { rememberMe = false }: AuthLoginOptions = {}) {
      // A new session supersedes any in-flight refresh from the previous one.
      sessionEpoch++
      clearTimer()
      const response = await auth.signIn(
        create(SignInRequestSchema, {
          identity: credentials.username,
          password: credentials.password,
          remember: rememberMe || undefined
        })
      )
      // The contract forks on multi-factor: no tokens are issued until the
      // second factor answers. The challenge flow ships later — refuse it
      // loudly here rather than half-support it.
      if (response.mfaRequired || response.mfaEnrollmentRequired) {
        tokens = null
        throw new ConnectError(
          'Multi-factor sign-in is not supported in this build yet.',
          Code.Unimplemented
        )
      }
      // A success with tokens always names its account; the field is
      // presence-based, so the guard keeps the mapping honest.
      if (!response.user) {
        throw new ConnectError('Sign-in answered without an account.', Code.Internal)
      }
      const pair = takeTokenPair(response)
      return { user: toProfile(response.user), tokens: pair }
    },

    refresh() {
      // Single-flight: concurrent callers share one in-flight refresh.
      if (refreshInFlight) return refreshInFlight
      // Cooldown: a refresh that just failed stays failed for a moment.
      if (Date.now() - lastFailedRefreshAt < REFRESH_COOLDOWN_MS) return Promise.resolve(false)
      refreshInFlight = doRefresh().finally(() => {
        refreshInFlight = null
      })
      return refreshInFlight
    },

    async maybeRefresh(withinMs) {
      if (tokens && Date.now() < tokens.accessExpiresAt - withinMs) return true
      return api.refresh()
    },

    async restore(next) {
      // Only a still-valid pair may be restored; a dead one is dropped so the
      // engine does not hold credentials the backend already expired — and so
      // the listener clears a cookie holding that dead pair.
      const accepted = next && next.refreshExpiresAt > Date.now() ? next : null
      tokens = accepted
      if (!accepted) notify(null)
      scheduleProactiveRefresh()
    },

    async session() {
      if (!tokens) return null
      // An expired access token is refreshed before the call — GetSession
      // carries the pair's own credential, so a proactive renewal is free.
      if (Date.now() >= tokens.accessExpiresAt) {
        const ok = await api.refresh()
        if (!ok || !tokens) return null
      }
      try {
        return readProfile(await readSession(tokens.accessToken))
      } catch (error) {
        // An unauthenticated answer means the access token died between the
        // expiry check and the call — one silent refresh and one retry. Any
        // other failure is not worth a second round trip: the bootstrap
        // reports no session either way.
        if (!(error instanceof ConnectError) || error.code !== Code.Unauthenticated) return null
        if (!(await api.refresh()) || !tokens) return null
        try {
          return readProfile(await readSession(tokens.accessToken))
        } catch {
          return null
        }
      }
    },

    async accessToken() {
      if (!tokens || Date.now() >= tokens.accessExpiresAt) return null
      return tokens.accessToken
    },

    async setTokenListener(listener) {
      tokenListener = listener
    },

    async logout() {
      // Invalidate any in-flight refresh first — its result must not land
      // after the session is gone.
      sessionEpoch++
      clearTimer()
      const spent = tokens
      tokens = null
      notify(null)
      if (!spent) return
      try {
        // The access token names the session SignOut ends. A network failure
        // still leaves the caller signed out locally — the pair is dropped
        // above and the refresh token simply lapses server-side.
        await session.signOut(create(SignOutRequestSchema), {
          headers: { authorization: `Bearer ${spent.accessToken}` }
        })
      } catch {
        // Session cleanup is not critical — the refresh token lapses on its own.
      }
    }
  }

  return api
}
