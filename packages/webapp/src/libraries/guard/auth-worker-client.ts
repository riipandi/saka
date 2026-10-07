import * as Comlink from 'comlink'
import { API_BASE_URL } from '#/libraries/api-client'
import type { LoginCredentials } from '#/schemas/auth.schema'
import type { User } from '#/schemas/user.schema'
import type { AuthEngineApi, AuthLoginOptions } from './auth-engine'
import { createAuthEngine } from './auth-engine'

/**
 * Promise-facing handle to the auth engine. Both the Comlink proxy and the
 * main-thread engine expose this surface: the proxy's extra Comlink marker
 * members are structurally irrelevant to callers, so the client type names
 * only the methods they can call.
 */
export interface AuthWorkerClient {
  /** Validate credentials and establish the cookie session. Resolves with the user profile. */
  login(credentials: LoginCredentials, options?: AuthLoginOptions): Promise<User>
  /** Silent refresh — single-flight. Resolves `true` when a session is established. */
  refresh(): Promise<boolean>
  /** Refresh only when the session expires within `withinMs`. Resolves `true` when still valid. */
  maybeRefresh(withinMs: number): Promise<boolean>
  /** Terminate the session server-side (the backend clears the HttpOnly cookies). */
  logout(): Promise<void>
}

let client: AuthWorkerClient | null = null

/**
 * Lazily create (once) the auth worker and return its typed proxy.
 *
 * Falls back to a main-thread engine when workers are unavailable —
 * non-browser environments (SSR, unit tests) or worker construction
 * failures (e.g. a restrictive CSP).
 */
export function authWorker(): AuthWorkerClient {
  if (client) return client

  // Skip workers under Vitest: happy-dom/node have no Worker implementation
  // and tests stub the network instead.
  const workerSupported = typeof Worker !== 'undefined' && !import.meta.env.VITEST

  if (workerSupported) {
    try {
      // Vite bundles this into a real worker file (CSP `worker-src 'self'`
      // compliant — no blob URLs).
      const worker = new Worker(new URL('./auth-token.worker.ts', import.meta.url), {
        type: 'module'
      })

      // If the worker dies mid-session (script error, extension interference),
      // swap in the main-thread engine so later calls still succeed.
      worker.addEventListener('error', () => {
        client = createMainThreadEngine()
      })

      client = Comlink.wrap<AuthEngineApi>(worker)
      return client
    } catch {
      // Worker construction failed — fall through to the main-thread engine.
    }
  }

  return createMainThreadEngine()
}

/**
 * The engine methods are already async, so the main-thread engine satisfies
 * the same promise-facing surface the Comlink proxy exposes.
 */
function createMainThreadEngine(): AuthWorkerClient {
  return createAuthEngine(API_BASE_URL)
}
