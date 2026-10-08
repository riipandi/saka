import * as Comlink from 'comlink'
import {
  clearTokenCookies,
  readTokenCookies,
  writeTokenCookies,
  writeUserCookie
} from './auth-cookies'
import type {
  AuthEngineApi,
  AuthLoginOptions,
  AuthSession,
  LoginCredentials,
  TokenListener
} from './auth-engine'
import { createAuthEngine } from './auth-engine'
import { clearAuth, type UserProfile } from './auth-store'
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
  /** Validate credentials, mint the pair, persist the cookie. Resolves the profile. */
  login(credentials: LoginCredentials, options?: AuthLoginOptions): Promise<UserProfile>
  /** Silent refresh — single-flight. Resolves `true` when a session is established. */
  refresh(): Promise<boolean>
  /** Refresh only when the access token expires within `withinMs`. Resolves `true` when still valid. */
  maybeRefresh(withinMs: number): Promise<boolean>
  /**
   * Hand the cookie-restored pair to the engine at boot. The main thread reads
   * the cookie and the worker takes custody of the tokens.
   */
  restore(): Promise<void>
  /** Rebuild the profile from the session the access token names, or null. */
  session(): Promise<UserProfile | null>
  /** The unexpired Bearer token for request interceptors, or null when absent. */
  accessToken(): Promise<string | null>
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

/**
 * The custody-change listener the engine reports to. A fresh pair rewrites
 * the cookie and is broadcast to the other tabs; a dropped pair clears both —
 * all in the same tick the engine took or lost custody, before anything else
 * can observe the stale copy.
 */
const persistTokens: TokenListener = (tokens) => {
  cachedAccess = tokens ? { token: tokens.accessToken, expiresAt: tokens.accessExpiresAt } : null
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
  return {
    async login(credentials: LoginCredentials, options?: AuthLoginOptions) {
      const result: AuthSession = await engine.login(credentials, options)
      writeUserCookie(result.user, result.tokens.refreshExpiresAt)
      return result.user
    },
    refresh: () => engine.refresh(),
    maybeRefresh: (withinMs) => engine.maybeRefresh(withinMs),
    async restore() {
      await engine.restore(readTokenCookies())
    },
    async session() {
      const profile = await engine.session()
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
    },
    accessToken: () => {
      if (cachedAccess && Date.now() < cachedAccess.expiresAt)
        return Promise.resolve(cachedAccess.token)
      return engine.accessToken()
    },
    async logout() {
      await engine.logout()
    }
  }
}

/** Wire the cookie persistence to the engine's custody changes. */
function wireCookies(engine: AuthEngineApi, proxied: boolean): void {
  void engine.setTokenListener(proxied ? Comlink.proxy(persistTokens) : persistTokens)
}

/**
 * Adopt the other tabs' custody changes: a pair another tab took or rotated
 * is adopted here (the engine re-reports it through the listener, and the
 * echo suppression ends the round trip); a sign-out elsewhere ends this
 * tab's session too — locally, without a round trip a spent token loses.
 */
function wireSync(engine: AuthEngineApi): void {
  unsubscribeSync?.()
  unsubscribeSync = subscribeTokens((tokens) => {
    if (tokens) {
      void engine.restore(tokens)
    } else {
      void engine.logout()
      clearAuth()
    }
  })
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
      wireSync(proxy)
      const wired = withCookies(proxy)
      client = wired

      // If the worker dies mid-session (script error, extension interference),
      // swap in the main-thread engine and re-restore from the cookie — the
      // dead worker's memory went with it, the cookie copy is what remains.
      worker.addEventListener(
        'error',
        () => {
          if (client !== wired) return
          const replacement = createMainThreadClient()
          client = replacement
          void replacement.restore()
        },
        { once: true }
      )

      return client
    } catch {
      // Worker construction failed — fall through to the main-thread engine.
    }
  }

  return createMainThreadClient()
}

/**
 * The engine methods are already async, so the main-thread engine satisfies
 * the same promise-facing surface the Comlink proxy exposes.
 */
function createMainThreadClient(): AuthWorkerClient {
  const engine = createAuthEngine()
  wireCookies(engine, false)
  wireSync(engine)
  return withCookies(engine)
}
