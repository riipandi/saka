import { createConnectTransport } from '@connectrpc/connect-web'
import { QueryClient } from '@tanstack/react-query'
import { ofetch, fetch } from 'ofetch'
import { clearAuth } from '#/libraries/guard/auth-store'
import { authWorker } from '#/libraries/guard/auth-worker-client'

/**
 * Base URL for API and RPC requests. Defaults to `/api` and `/rpc`.
 * These are the same-origin Vite dev proxy, see `vite.config.ts`.
 */
export const RPC_BASE_URL = import.meta.env.PUBLIC_RPC_URL ?? '/rpc'
export const API_BASE_URL = import.meta.env.PUBLIC_API_URL ?? '/api'

export const queryClient = new QueryClient({
  defaultOptions: {
    mutations: {
      retry: 0
    },
    queries: {
      refetchOnWindowFocus: false,
      staleTime: 1000 * 60 * 5,
      retry: 1
    }
  }
})

/**
 * The Bearer header for outgoing calls, resolved from the auth worker. The
 * worker holds the live pair; it answers null when no unexpired token exists,
 * so anonymous calls (SignIn, Refresh) go out headerless instead of carrying
 * a stale credential the guard could refuse.
 */
async function bearerHeader(): Promise<Record<string, string>> {
  const token = await authWorker().accessToken()
  return token ? { authorization: `Bearer ${token}` } : {}
}

/**
 * - Injects the Bearer access token the auth worker holds, when it has one.
 * - On 401, performs a single-flight silent refresh via the auth worker and
 *   retries the request.
 * - Base URL from `PUBLIC_API_URL` envar defaults to `/api`.
 *
 * All backend API calls should import `api` from here.
 */
export const api = ofetch.create({
  baseURL: API_BASE_URL,
  credentials: 'include',
  retry: 1,
  retryStatusCodes: [401],
  async onRequest({ options }) {
    options.headers = new Headers(options.headers)
    for (const [name, value] of Object.entries(await bearerHeader())) {
      options.headers.set(name, value)
    }
  },
  async onResponseError({ response }) {
    if (response.status !== 401) return
    const refreshed = await authWorker().refresh()
    if (!refreshed) {
      clearAuth()
    }
  }
})

/**
 * The ConnectRPC transport defines what type of endpoint we're hitting.
 * ConnectRPC base URL defaults to `/rpc` — the same-origin dev proxy.
 * In production, point `PUBLIC_RPC_URL`, same parent domain so the requests
 * stay first-party against the Bearer credential the auth worker holds.
 */
export const rpcTransport = createConnectTransport({
  baseUrl: RPC_BASE_URL,
  fetch: async (input, init) => {
    const headers = new Headers(init?.headers)
    for (const [name, value] of Object.entries(await bearerHeader())) {
      headers.set(name, value)
    }
    return fetch(input, { ...init, headers })
  }
})
