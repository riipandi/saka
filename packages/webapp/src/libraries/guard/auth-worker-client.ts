import * as Comlink from 'comlink'
import { deviceHeaders } from '#/libraries/device-fingerprint'
import { spanOperation } from '#/libraries/telemetry/seam-span'
import { decodeAccessClaims } from './auth-claims'
import {
  clearTokenCookies,
  readTokenCookies,
  writeTokenCookies,
  writeUserCookie
} from './auth-cookies'
import type {
  AuthEngineApi,
  AuthLoginOptions,
  CompleteSignInFactor,
  LoginCredentials,
  SignInOutcome,
  TokenBundle,
  TokenListener
} from './auth-engine'
import { createAuthEngine } from './auth-engine'
import { clearAuth, setAuthGrants, type UserProfile } from './auth-store'
import { publishTokens, subscribeTokens } from './auth-sync'

/**
 * Promise-facing handle to the auth engine. Both the Comlink proxy and the
 * main-thread engine expose this surface.
 *
 * The cookie persistence lives HERE, on the main thread — workers have no
 * `document.cookie` access — and is driven entirely by the engine's token
 * listener: every custody change the engine reports (sign-in, rotation by
 * the proactive timer, failed refresh, sign-out) is mirrored into the cookie
 * and broadcast to the other tabs the moment it happens. There is
 * deliberately no other write path, so the cookie cannot lag behind the
 * worker's live pair.
 */
export interface AuthWorkerClient {
  /**
   * Validate credentials. Resolves the sign-in's outcome — the profile when
   * the session was established, or the multi-factor fork the view finishes
   * (the pair's custody happened inside, the cookie is written only for a
   * signed-in answer).
   */
  login(credentials: LoginCredentials, options?: AuthLoginOptions): Promise<SignInOutcome>
  /** Complete an OAuth SSO flow the callback redirected with. Resolves the same outcome. */
  continueSignIn(flowToken: string): Promise<SignInOutcome>
  /** Spend the pending bridge with the second factor. Resolves the profile. */
  completeSignIn(pendingToken: string, factor: CompleteSignInFactor): Promise<UserProfile>
  /** Finish a discoverable passkey sign-in. Resolves the profile. */
  verifyPasskeyLogin(sessionId: string, credential: string): Promise<UserProfile>
  /** Ask the backend to email a one-time code. Resolves the device token to pair at the exchange. */
  requestOneTimeAccess(email: string): Promise<string>
  /** Spend a one-time code. Resolves the outcome — signed-in or the MFA fork the view finishes. */
  exchangeOneTimeToken(token: string, deviceToken?: string): Promise<SignInOutcome>
  /**
   * End the impersonation the current pair carries: the backend answers the
   * administrator's own fresh pair and the custody change rides the same
   * listener the sign-ins report through. Resolves the administrator's
   * profile.
   */
  stopImpersonation(): Promise<UserProfile>
  /**
   * Begin a delegation onto the named account. Resolves the target's
   * profile; the custody change rides the same listener the sign-ins
   * report through, and the banner is the browser's evidence of the new
   * session.
   */
  impersonate(userId: string, reason: string): Promise<UserProfile>
  /** Silent refresh — single-flight. Resolves `true` when a session is established. */
  refresh(): Promise<boolean>
  /** Refresh only when the access token expires within `withinMs`. Resolves `true` when still valid. */
  maybeRefresh(withinMs: number): Promise<boolean>
  /**
   * Hand the cookie-restored pair to the engine at boot. The main thread reads
   * the cookie and the worker takes custody of the tokens. An explicit
   * bundle — the cross-tab adoption — skips the jar read: the broadcast
   * arrives before the receiving tab's cookie view may.
   */
  restore(bundle?: TokenBundle): Promise<void>
  /** Rebuild the profile from the session the access token names, or null. */
  session(): Promise<UserProfile | null>
  /** The unexpired Bearer token for request interceptors, or null when absent. */
  accessToken(): Promise<string | null>
  /**
   * The Bearer header for outgoing requests, refreshing proactively when the
   * access token expires within the request margin. Resolves empty for an
   * anonymous caller.
   */
  authorization(): Promise<Record<string, string>>
  /** Terminate the session server-side and clear the cookie. */
  logout(): Promise<void>
}

let client: AuthWorkerClient | null = null
let unsubscribeSync: (() => void) | null = null

/**
 * The unexpired access token, kept on the main thread so request interceptors
 * do not pay a Comlink round trip per call. Warmed by the same custody-change
 * listener that writes the cookies; the engine is consulted when the cache is
 * empty (a pair restored before the listener reported it) or expired.
 */
let cachedAccess: { token: string; expiresAt: number } | null = null

/** Request-time margin ahead of expiry — past it, refresh before sending. */
const REQUEST_REFRESH_MARGIN_MS = 30_000

/**
 * The custody-change listener the engine reports to. A fresh pair rewrites
 * the cookie and is broadcast to the other tabs; a dropped pair clears both —
 * all in the same tick the engine took or lost custody, before anything else
 * can observe the stale copy.
 */
const persistTokens: TokenListener = (tokens) => {
  cachedAccess = tokens ? { token: tokens.accessToken, expiresAt: tokens.accessExpiresAt } : null
  // The grant snapshot rides the same custody tick: a fresh pair replaces
  // the store's claims before anything can render the stale ones, and the
  // drop clears them with the pair.
  setAuthGrants(tokens ? decodeAccessClaims(tokens.accessToken) : null)
  if (tokens) {
    writeTokenCookies(tokens)
  } else {
    clearTokenCookies()
  }
  publishTokens(tokens)
}

/**
 * Wrap an engine (worker proxy or main-thread fallback) with the cookie
 * persistence and tab sync both paths need.
 */
function withCookies(engine: AuthEngineApi): AuthWorkerClient {
  // The device headers are computed once on the main thread — the worker
  // realm has none of the fingerprinting sources — and handed to the engine
  // before its first server call: the session-establishing methods below
  // await this promise, so the sign-in itself already carries the
  // fingerprint the server's records consume.
  const deviceReady: Promise<void> = deviceHeaders().then((headers) =>
    engine.configureDevice(headers)
  )

  return {
    async login(credentials: LoginCredentials, options?: AuthLoginOptions) {
      return spanOperation('login', async (hints) => {
        await deviceReady
        const outcome: SignInOutcome = await engine.login(credentials, options, hints)
        // Only a signed-in answer establishes custody the cookie mirrors; a
        // fork carries no tokens and leaves the jar exactly as it was.
        if (outcome.kind === 'signed-in') {
          writeUserCookie(outcome.session.user, outcome.session.tokens.refreshExpiresAt)
        }
        return outcome
      })
    },
    async continueSignIn(flowToken: string) {
      return spanOperation('continue-sign-in', async (hints) => {
        await deviceReady
        const outcome: SignInOutcome = await engine.continueSignIn(flowToken, hints)
        if (outcome.kind === 'signed-in') {
          writeUserCookie(outcome.session.user, outcome.session.tokens.refreshExpiresAt)
        }
        return outcome
      })
    },
    async completeSignIn(pendingToken: string, factor: CompleteSignInFactor) {
      return spanOperation('complete-sign-in', async (hints) => {
        await deviceReady
        const session = await engine.completeSignIn(pendingToken, factor, hints)
        writeUserCookie(session.user, session.tokens.refreshExpiresAt)
        return session.user
      })
    },
    async verifyPasskeyLogin(sessionId: string, credential: string) {
      return spanOperation('verify-passkey-login', async (hints) => {
        await deviceReady
        const session = await engine.verifyPasskeyLogin(sessionId, credential, hints)
        writeUserCookie(session.user, session.tokens.refreshExpiresAt)
        return session.user
      })
    },
    async requestOneTimeAccess(email: string) {
      return spanOperation('request-one-time-access', async (hints) => {
        await deviceReady
        return engine.requestOneTimeAccess(email, hints)
      })
    },
    async exchangeOneTimeToken(token: string, deviceToken?: string) {
      return spanOperation('exchange-one-time-token', async (hints) => {
        await deviceReady
        const outcome: SignInOutcome = await engine.exchangeOneTimeToken(token, deviceToken, hints)
        // The same rule the password sign-in keeps: only a signed-in answer
        // establishes custody the cookie mirrors.
        if (outcome.kind === 'signed-in') {
          writeUserCookie(outcome.session.user, outcome.session.tokens.refreshExpiresAt)
        }
        return outcome
      })
    },
    async stopImpersonation() {
      return spanOperation('stop-impersonation', async (hints) => {
        await deviceReady
        const session = await engine.stopImpersonation(hints)
        // The administrator's own pair is a fresh custody the cookie mirrors,
        // exactly as a sign-in's would be.
        writeUserCookie(session.user, session.tokens.refreshExpiresAt)
        return session.user
      })
    },
    async impersonate(userId: string, reason: string) {
      return spanOperation('impersonate', async (hints) => {
        await deviceReady
        const session = await engine.impersonate(userId, reason, hints)
        // The target's pair is a fresh custody the cookie mirrors, exactly
        // as a sign-in's would be.
        writeUserCookie(session.user, session.tokens.refreshExpiresAt)
        return session.user
      })
    },
    async refresh() {
      return spanOperation('refresh', async (hints) => {
        await deviceReady
        return engine.refresh(hints)
      })
    },
    async maybeRefresh(withinMs: number) {
      return spanOperation('maybe-refresh', async (hints) => {
        await deviceReady
        return engine.maybeRefresh(withinMs, hints)
      })
    },
    async restore(bundle?: TokenBundle) {
      return spanOperation('restore', async () => {
        await deviceReady
        await engine.restore(bundle ?? readTokenCookies())
      })
    },
    async session() {
      return spanOperation('session', async (hints) => {
        const profile = await engine.session(hints)
        if (profile) {
          // Cache the confirmed profile for the next reload, with the refresh
          // half's lifetime — the pair (possibly just rotated) is in the jar.
          writeUserCookie(profile, readTokenCookies()?.refreshExpiresAt)
        } else {
          // A definite sign-out clears the jar — but only when the engine holds
          // no pair: a custody change that raced this call (a re-login, a
          // rotation) owns the jar now and must not be cleared on its behalf.
          if (!(await engine.accessToken())) clearTokenCookies()
        }
        return profile
      })
    },
    accessToken: () => {
      if (cachedAccess && Date.now() < cachedAccess.expiresAt)
        return Promise.resolve(cachedAccess.token)
      return engine.accessToken()
    },
    async authorization() {
      // A fresh cached token answers without a worker round trip; near (or
      // past) expiry the engine's single-flight refresh runs first.
      if (cachedAccess && Date.now() < cachedAccess.expiresAt - REQUEST_REFRESH_MARGIN_MS) {
        return { authorization: `Bearer ${cachedAccess.token}` }
      }
      await engine.maybeRefresh(REQUEST_REFRESH_MARGIN_MS)
      const token = await engine.accessToken()
      const headers: Record<string, string> = {}
      if (token) headers.authorization = `Bearer ${token}`
      return headers
    },
    async logout() {
      return spanOperation('logout', async (hints) => {
        await engine.logout(hints)
      })
    }
  }
}

/** Wire the cookie persistence to the engine's custody changes. */
function wireCookies(engine: AuthEngineApi, proxied: boolean): void {
  void engine.setTokenListener(proxied ? Comlink.proxy(persistTokens) : persistTokens)
}

/**
 * Adopt the other tabs' custody changes through the cell: a pair another
 * tab took or rotated is adopted here (the engine re-reports it through
 * the listener, and the echo suppression ends the round trip); a sign-out
 * elsewhere ends this tab's session too — locally, without a round trip a
 * spent token loses. Riding the cell is the point: an adoption a dying
 * worker interrupted re-runs against the replacement instead of hanging
 * on the dead proxy forever.
 */
function wireSync(cell: EngineCell): void {
  unsubscribeSync?.()
  unsubscribeSync = subscribeTokens((tokens) => {
    if (tokens) {
      // The bundle rides the broadcast on purpose: the other tab wrote the
      // cookie just before publishing, and the receiving tab's cookie view
      // may lag the message — the jar read could restore a stale pair.
      const bundle = tokens
      void cell.run((current) => current.restore(bundle))
    } else {
      void cell.run((current) => current.logout())
      clearAuth()
    }
  })
}

/**
 * The margin one engine call may take. A worker realm that dies without
 * an error event leaves its promises unsettled forever; the bound turns
 * that hang into a rejection the caller can recover from. Generous on
 * purpose — these are short round trips, not uploads.
 */
const CALL_BOUND_MS = 30_000

/**
 * The current-engine cell — the indirection the dead-worker recovery
 * needs. Callers hold the cell, never an engine: every call starts
 * against the live client, an in-flight call the engine never settles is
 * re-run against the replacement on `revive` (the dead proxy's memory
 * went with it; the cookie copy is what remains), and a call neither the
 * engine nor an error event answers rejects at the bound.
 */
interface EngineCell {
  run: <T>(task: (current: AuthWorkerClient) => Promise<T>) => Promise<T>
  /** Swap in the replacement and re-run every call the dead engine never settled. */
  revive: (replacement: AuthWorkerClient) => void
}

export function createEngineCell(
  initial: AuthWorkerClient,
  boundMs: number = CALL_BOUND_MS
): EngineCell {
  let current = initial
  // The attempts no engine has settled yet. A healthy call removes itself
  // on settle; a dead proxy leaves its attempt here forever, and `revive`
  // is what gives it an answer.
  const unsettled = new Set<(target: AuthWorkerClient) => void>()

  return {
    run(task) {
      return new Promise((resolve, reject) => {
        const attempt = (target: AuthWorkerClient) => {
          const timer = setTimeout(() => {
            unsettled.delete(attempt)
            reject(new Error(`the auth engine did not answer within ${boundMs}ms`))
          }, boundMs)
          task(target).then(
            (value) => {
              clearTimeout(timer)
              unsettled.delete(attempt)
              resolve(value)
            },
            (error) => {
              clearTimeout(timer)
              unsettled.delete(attempt)
              reject(error)
            }
          )
        }
        unsettled.add(attempt)
        attempt(current)
      })
    },
    revive(replacement) {
      current = replacement
      const interrupted = [...unsettled]
      unsettled.clear()
      for (const attempt of interrupted) attempt(replacement)
    }
  }
}

/**
 * The promise-facing surface the app holds: every method rides the cell
 * instead of a captured engine, so the next call picks up a swapped
 * engine and `revive` serves an interrupted one. The spans and the cookie
 * persistence stay on the wrapped client — the facade only routes.
 */
function createBoundedClient(cell: EngineCell): AuthWorkerClient {
  wireSync(cell)
  return {
    login: (credentials, options) => cell.run((current) => current.login(credentials, options)),
    continueSignIn: (flowToken) => cell.run((current) => current.continueSignIn(flowToken)),
    completeSignIn: (pendingToken, factor) =>
      cell.run((current) => current.completeSignIn(pendingToken, factor)),
    verifyPasskeyLogin: (sessionId, credential) =>
      cell.run((current) => current.verifyPasskeyLogin(sessionId, credential)),
    requestOneTimeAccess: (email) => cell.run((current) => current.requestOneTimeAccess(email)),
    exchangeOneTimeToken: (token, deviceToken) =>
      cell.run((current) => current.exchangeOneTimeToken(token, deviceToken)),
    stopImpersonation: () => cell.run((current) => current.stopImpersonation()),
    impersonate: (userId, reason) => cell.run((current) => current.impersonate(userId, reason)),
    refresh: () => cell.run((current) => current.refresh()),
    maybeRefresh: (withinMs) => cell.run((current) => current.maybeRefresh(withinMs)),
    restore: () => cell.run((current) => current.restore()),
    session: () => cell.run((current) => current.session()),
    accessToken: () => cell.run((current) => current.accessToken()),
    authorization: () => cell.run((current) => current.authorization()),
    logout: () => cell.run((current) => current.logout())
  }
}

/**
 * The main-thread engine, fully wired: the cookie persistence rides the
 * engine's custody changes, and the wrapped client is what the cell holds.
 */
function createMainThreadEngine(): AuthWorkerClient {
  const engine = createAuthEngine()
  wireCookies(engine, false)
  return withCookies(engine)
}

/**
 * Lazily create (once) the auth worker and return its typed proxy.
 *
 * Falls back to a main-thread engine when workers are unavailable —
 * non-browser environments (SSR, unit tests) or worker construction
 * failures (e.g. a restrictive CSP).
 */
export function authWorker(): AuthWorkerClient {
  if (client) return client

  // Skip workers under Vitest: the stubbed network lives in the test realm,
  // not inside a worker realm — `import.meta.env.VITEST` is not effective
  // inside the worker chunk, so the test mode itself is the gate.
  const workerSupported = typeof Worker !== 'undefined' && import.meta.env.MODE !== 'test'

  if (workerSupported) {
    try {
      // Vite bundles this into a real worker file (CSP `worker-src 'self'`
      // compliant — no blob URLs).
      const worker = new Worker(new URL('./auth-token.worker.ts', import.meta.url), {
        type: 'module'
      })

      const proxy = Comlink.wrap<AuthEngineApi>(worker)
      wireCookies(proxy, true)
      const cell = createEngineCell(withCookies(proxy))
      client = createBoundedClient(cell)

      // If the worker dies mid-session (script error, extension
      // interference), the cookie copy is what remains: the replacement is
      // a fully wired main-thread engine, the cell re-runs every call the
      // dead proxy never answered against it, and an idle boot
      // re-establishes custody from the cookie.
      worker.addEventListener(
        'error',
        () => {
          const replacement = createMainThreadEngine()
          cell.revive(replacement)
          void replacement.restore()
        },
        { once: true }
      )

      return client
    } catch {
      // Worker construction failed — fall through to the main-thread engine.
    }
  }

  client = createBoundedClient(createEngineCell(createMainThreadEngine()))
  return client
}
