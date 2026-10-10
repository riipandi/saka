import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import { Code, ConnectError, createClient } from '@connectrpc/connect'
import { createConnectTransport } from '@connectrpc/connect-web'
import { RPC_BASE_URL } from '#/libraries/api-client'
import { AuthService, GetSessionRequestSchema, RefreshRequestSchema } from '~/codegen/authn_pb'
import {
  CompleteSignInRequestSchema,
  ContinueOAuthSignInRequestSchema,
  MultifactorService,
  OAuthSSOService,
  PasskeyFactorSchema,
  SessionService,
  SignInRequestSchema,
  SignOutRequestSchema
} from '~/codegen/authn_pb'
import type {
  AuthenticatedUser,
  CompleteSignInResponse,
  ContinueOAuthSignInResponse,
  GetSessionResponse,
  RefreshResponse,
  SignInRequest,
  SignInResponse
} from '~/codegen/authn_pb'
import type { TraceHints } from '../telemetry/seam-span'
import type { UserProfile } from './auth-store'

/** Refresh this long before the access token expires. */
const PROACTIVE_MARGIN_MS = 60_000

/** Skip repeated refresh attempts within this window after a failure. */
const REFRESH_COOLDOWN_MS = 5_000

/** setTimeout ceiling — delays above 2^31-1 ms overflow to ~0 in browsers. */
const MAX_TIMER_DELAY_MS = 2_147_483_647

/** The Connect call options the hints produce: the trace context rides the
 * call's headers, so concurrent calls carry their own context and none
 * reads another's. Undefined while the tracer is off. */
function callHints(hints?: TraceHints): { headers: Record<string, string> } | undefined {
  return hints?.traceparent ? { headers: { traceparent: hints.traceparent } } : undefined
}

/** Merge the hints into headers an existing call option already carries. */
function withHints(headers: Record<string, string>, hints?: TraceHints): Record<string, string> {
  return hints?.traceparent ? { ...headers, traceparent: hints.traceparent } : headers
}

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
 * What the sign-in form collects — the wire's own `SignInRequest` shape
 * (the form field is the identity, and `remember` travels separately as a
 * login option, so the pair is exactly the required fields).
 */
export type LoginCredentials = Pick<SignInRequest, 'identity' | 'password'>

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
 * A sign-in the backend paused at the multi-factor fork: no tokens were
 * issued, the pending bridge is the only credential the caller holds, and it
 * dies at `expiresAt` (epoch milliseconds) unspent. The bridge lives in the
 * view's memory alone — never a URL — until the second factor spends it.
 */
export type SignInChallenge =
  | {
      kind: 'mfa-challenge'
      pendingToken: string
      expiresAt: number
    }
  | {
      kind: 'enrollment-required'
      pendingToken: string
      expiresAt: number
    }

/**
 * The sign-in's outcome: either the session the pair establishes, or the
 * fork the flow paused at (D8 — a discriminated outcome, not an exception
 * the view steers by).
 */
export type SignInOutcome = { kind: 'signed-in'; session: AuthSession } | SignInChallenge

/** The second factor `completeSignIn` spends the bridge with. */
export type CompleteSignInFactor =
  | { kind: 'code'; code: string }
  | { kind: 'passkey'; sessionId: string; credential: string }

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
  /**
   * Validate credentials. Resolves the outcome: the session's profile and
   * tokens, or the multi-factor fork the flow paused at — a challenge to
   * complete through {@link AuthEngineApi.completeSignIn}, or a forced
   * enrollment the pending bridge admits.
   */
  login(
    credentials: LoginCredentials,
    options?: AuthLoginOptions,
    hints?: TraceHints
  ): Promise<SignInOutcome>
  /**
   * Complete an OAuth SSO flow the callback redirected with: the flow token
   * is the whole credential. Resolves the same outcome the password sign-in
   * does; a flow that paused at a stage this build does not handle is
   * refused loudly.
   */
  continueSignIn(flowToken: string, hints?: TraceHints): Promise<SignInOutcome>
  /**
   * Spend the pending bridge with the second factor — a TOTP or recovery
   * code, or a passkey assertion over a ceremony the sign-in begin answered.
   * Resolves the profile and tokens the way a one-factor sign-in does.
   */
  completeSignIn(
    pendingToken: string,
    factor: CompleteSignInFactor,
    hints?: TraceHints
  ): Promise<AuthSession>
  /** Silent refresh — single-flight. Resolves `true` when a session is established. */
  refresh(hints?: TraceHints): Promise<boolean>
  /** Refresh only when the access token expires within `withinMs`. Resolves `true` when still valid. */
  maybeRefresh(withinMs: number, hints?: TraceHints): Promise<boolean>
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
  session(hints?: TraceHints): Promise<UserProfile | null>
  /** The unexpired Bearer token for request interceptors, or null when absent. */
  accessToken(): Promise<string | null>
  /**
   * Register the custody-change listener (see {@link TokenListener}). The
   * main thread passes a `Comlink.proxy` callback; the worker engine calls it
   * across the boundary on every pair it takes or drops.
   */
  setTokenListener(listener: TokenListener | null): Promise<void>
  /**
   * Hand the device headers (fingerprint, user agent) the engine's transport
   * merges into every request. The main thread computes them — the worker
   * realm has none of the fingerprinting sources — and passes them in at
   * boot; the server's audit records and session rows read what lands.
   */
  configureDevice(headers: Record<string, string>): Promise<void>
  /** Terminate the session server-side and drop the in-memory pair. */
  logout(hints?: TraceHints): Promise<void>
}

/**
 * The profile the store and the cookie jar carry — the wire account's own
 * four fields and nothing else. The copy is the point: a proto message
 * object travels with its `$typeName` internals, and neither the store nor
 * the JSON cookie may hold them.
 */
function toProfile(user: AuthenticatedUser): UserProfile {
  const { id, username, email, displayName } = user
  return { id, username, email, displayName }
}

/** The GetSession answer's account, mapped — the field is presence-based. */
function readProfile(response: GetSessionResponse): UserProfile | null {
  return response.user ? toProfile(response.user) : null
}

/** The pending bridge's mint-time lifetime when the wire omits its expiry —
 * the backend keeps it five minutes (see `.llms/rules.md`, Multifactor). */
const MFA_BRIDGE_FALLBACK_MS = 5 * 60_000

/** The fork the sign-in paused at, read off the wire's fork fields. The
 * enrollment fork precedes the challenge fork: the enrollment answer carries
 * the same bridge and the same expiry. */
function readFork(response: SignInResponse | ContinueOAuthSignInResponse): SignInChallenge | null {
  const expiresAt = response.mfaPendingExpiresAt
    ? timestampDate(response.mfaPendingExpiresAt).getTime()
    : Date.now() + MFA_BRIDGE_FALLBACK_MS
  if ('mfaEnrollmentRequired' in response && response.mfaEnrollmentRequired) {
    return { kind: 'enrollment-required', pendingToken: response.mfaPendingToken, expiresAt }
  }
  if (response.mfaRequired && response.mfaPendingToken) {
    return { kind: 'mfa-challenge', pendingToken: response.mfaPendingToken, expiresAt }
  }
  return null
}

export function createAuthEngine(baseUrl: string = RPC_BASE_URL): AuthEngineApi {
  /**
   * The device headers the main thread handed over. They start empty — the
   * main-thread wrapper awaits their computation before the first
   * session-establishing call — and once set they ride every request the
   * engine's transport makes.
   */
  let deviceHeaders: Record<string, string> = {}

  const transport = createConnectTransport({
    baseUrl,
    fetch: (input, init) => {
      const merged = new Headers(init?.headers)
      for (const [name, value] of Object.entries(deviceHeaders)) merged.set(name, value)
      return fetch(input, { ...init, headers: merged })
    }
  })
  const auth = createClient(AuthService, transport)
  const session = createClient(SessionService, transport)
  const oauth = createClient(OAuthSSOService, transport)
  const mfa = createClient(MultifactorService, transport)

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
  function readSession(bearer: string, hints?: TraceHints) {
    return session.getSession(create(GetSessionRequestSchema), {
      headers: withHints({ authorization: `Bearer ${bearer}` }, hints)
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
  /** Take custody of a fresh pair from SignIn, ContinueSignIn, or Refresh and return it. */
  function takeTokenPair(
    response:
      | SignInResponse
      | RefreshResponse
      | ContinueOAuthSignInResponse
      | CompleteSignInResponse
  ): TokenBundle {
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

  /** The custody path every sign-in completion rides — the fork's answer
   * and the one-factor sign-in land here alike. A success with tokens always
   * names its account; the field is presence-based, so the guard keeps the
   * mapping honest. */
  function finishSignIn(
    response: SignInResponse | ContinueOAuthSignInResponse | CompleteSignInResponse
  ): AuthSession {
    if (!response.user) {
      throw new ConnectError('Sign-in answered without an account.', Code.Internal)
    }
    const pair = takeTokenPair(response)
    return { user: toProfile(response.user), tokens: pair }
  }

  async function doRefresh(hints?: TraceHints): Promise<boolean> {
    if (!tokens?.refreshToken) return false
    // Snapshot the generation: if logout (or a fresh login) happens while the
    // request is in flight, its result must not resurrect the old session.
    const epoch = sessionEpoch
    try {
      const response = await session.refresh(
        create(RefreshRequestSchema, { refreshToken: tokens.refreshToken }),
        callHints(hints)
      )
      if (epoch !== sessionEpoch) return false
      takeTokenPair(response)
      return true
    } catch (error) {
      if (epoch !== sessionEpoch) return false
      // Cooldown applies to every failure — the retry must not hammer a
      // backend that is refusing or unreachable.
      lastFailedRefreshAt = Date.now()
      // A refused refresh (the backend saw the pair and said no) means the
      // pair is spent or revoked — drop it so a later attempt does not replay
      // a dead token; the listener clears the cookie copy in the same breath.
      // A network failure is inconclusive: the backend never judged the
      // pair, so it stays — the proactive timer retries after the cooldown.
      if (error instanceof ConnectError && error.code === Code.Unauthenticated) {
        tokens = null
        clearTimer()
        notify(null)
      }
      return false
    }
  }

  const api: AuthEngineApi = {
    async login(credentials, { rememberMe = false }: AuthLoginOptions = {}, hints) {
      // A new session supersedes any in-flight refresh from the previous one.
      sessionEpoch++
      clearTimer()
      const response = await auth.signIn(
        create(SignInRequestSchema, {
          ...credentials,
          remember: rememberMe || undefined
        }),
        callHints(hints)
      )
      // The contract forks on multi-factor: no tokens are issued until the
      // second factor answers (or the forced enrollment completes). The fork
      // is the outcome — the bridge rides the view's memory alone.
      const fork = readFork(response)
      if (fork) {
        tokens = null
        return fork
      }
      return { kind: 'signed-in', session: finishSignIn(response) }
    },

    async continueSignIn(flowToken, hints) {
      // A new session supersedes any in-flight refresh from the previous one.
      sessionEpoch++
      clearTimer()
      const response = await oauth.continueSignIn(
        create(ContinueOAuthSignInRequestSchema, { flowToken }),
        callHints(hints)
      )
      // The same fork the password sign-in pauses with. A stage pause this
      // build does not handle is still refused loudly rather than
      // half-supported.
      const fork = readFork(response)
      if (fork) {
        tokens = null
        return fork
      }
      if (response.stage) {
        tokens = null
        throw new ConnectError(
          `The sign-in flow paused at ${response.stage}, which this build does not handle yet.`,
          Code.Unimplemented
        )
      }
      return { kind: 'signed-in', session: finishSignIn(response) }
    },

    async completeSignIn(pendingToken, factor, hints) {
      const response = await mfa.completeSignIn(
        create(CompleteSignInRequestSchema, {
          pendingToken,
          secondFactor:
            factor.kind === 'code'
              ? { case: 'code', value: factor.code }
              : {
                  case: 'passkey',
                  value: create(PasskeyFactorSchema, {
                    sessionId: factor.sessionId,
                    credential: factor.credential
                  })
                }
        }),
        callHints(hints)
      )
      return finishSignIn(response)
    },

    refresh(hints) {
      // Single-flight: concurrent callers share one in-flight refresh.
      if (refreshInFlight) return refreshInFlight
      // Cooldown: a refresh that just failed stays failed for a moment.
      if (Date.now() - lastFailedRefreshAt < REFRESH_COOLDOWN_MS) return Promise.resolve(false)
      refreshInFlight = doRefresh(hints).finally(() => {
        refreshInFlight = null
      })
      return refreshInFlight
    },

    async maybeRefresh(withinMs, hints) {
      if (tokens && Date.now() < tokens.accessExpiresAt - withinMs) return true
      return api.refresh(hints)
    },

    async restore(next) {
      // Only a still-valid pair may be restored; a dead one is dropped so the
      // engine does not hold credentials the backend already expired — and so
      // the listener clears a cookie holding that dead pair.
      const accepted = next && next.refreshExpiresAt > Date.now() ? next : null
      tokens = accepted
      // An accepted pair is reported too: the rewrite is idempotent and the
      // report warms the main thread's token cache after a restore.
      notify(accepted)
      scheduleProactiveRefresh()
    },

    async session(hints) {
      if (!tokens) return null
      // An expired access token is refreshed before the call — GetSession
      // carries the pair's own credential, so a proactive renewal is free.
      if (Date.now() >= tokens.accessExpiresAt) {
        const ok = await api.refresh(hints)
        if (!ok || !tokens) return null
      }
      try {
        return readProfile(await readSession(tokens.accessToken, hints))
      } catch (error) {
        // An unauthenticated answer means the access token died between the
        // expiry check and the call — one silent refresh and one retry; the
        // backend has judged the pair, so this is a definite answer.
        if (error instanceof ConnectError && error.code === Code.Unauthenticated) {
          if (!(await api.refresh()) || !tokens) return null
          try {
            return readProfile(await readSession(tokens.accessToken))
          } catch {
            return null
          }
        }
        // Anything else — network trouble, an unavailable backend — is
        // inconclusive: the session was neither confirmed nor refused.
        // Rethrow so the caller can keep the state it already shows instead
        // of reading a connection failure as a sign-out.
        throw error
      }
    },

    async accessToken() {
      if (!tokens || Date.now() >= tokens.accessExpiresAt) return null
      return tokens.accessToken
    },

    async setTokenListener(listener) {
      tokenListener = listener
    },

    async configureDevice(headers) {
      deviceHeaders = { ...headers }
    },

    async logout(hints) {
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
          headers: withHints({ authorization: `Bearer ${spent.accessToken}` }, hints)
        })
      } catch {
        // Session cleanup is not critical — the refresh token lapses on its own.
      }
    }
  }

  return api
}
