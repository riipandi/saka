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
 * - Sends the HttpOnly cookie session with every request (`credentials: 'include'`).
 * - On 401, performs a single-flight silent refresh via the auth worker and
 *   retries the request (the browser attaches the fresh cookie automatically).
 * - Base URL from `PUBLIC_API_URL` envar defaults to `/api`.
 *
 * All backend API calls should import `api` from here.
 */
export const api = ofetch.create({
  baseURL: API_BASE_URL,
  credentials: 'include',
  retry: 1,
  retryStatusCodes: [401],
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
 * ConnectRPC base URL defaults to `/rpc` — the same-origin Vite dev proxy.
 * In production, point `PUBLIC_RPC_URL`, same parent domain so the
 * HttpOnly session cookies are first-party.
 */
export const rpcTransport = createConnectTransport({
  baseUrl: RPC_BASE_URL,
  fetch: fetch
})
