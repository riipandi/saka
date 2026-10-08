import { createConnectTransport } from '@connectrpc/connect-web'
import { QueryClient } from '@tanstack/react-query'
import { ofetch } from 'ofetch'
import { clearAuth } from '#/libraries/guard/auth-store'
import { authWorker } from '#/libraries/guard/auth-worker-client'

/**
 * Base URL for API and RPC requests. Defaults to `/api` and `/rpc`.
 * These are the same-origin Vite dev proxy, see `vite.config.ts`.
 */
export const API_BASE_URL = import.meta.env.PUBLIC_API_URL ?? '/api'
export const RPC_BASE_URL = import.meta.env.PUBLIC_RPC_URL ?? '/rpc'

export const queryClient = new QueryClient({
  defaultOptions: {
    mutations: { retry: 0 },
    queries: {
      refetchOnWindowFocus: false,
      staleTime: 1000 * 60 * 5,
      retry: 1
    }
  }
})

/**
 * The 401 recovery: one single-flight silent refresh and one replay. A
 * refresh the backend refused leaves the session only when the worker holds
 * no pair any more; a network failure is inconclusive and falls through with
 * the original 401 for the caller to handle.
 */
async function recoverFrom401(): Promise<boolean> {
  if (!(await authWorker().refresh())) {
    if (!(await authWorker().accessToken())) clearAuth()
    return false
  }
  return true
}

/**
 * The seam both clients ride: injects the Bearer the worker holds, refreshes
 * proactively near expiry, and on a 401 refreshes once and replays once. The
 * refresh itself travels on the engine's own transport, so a replay can
 * never recurse into the seam.
 */
async function withAuth(send: (headers: Record<string, string>) => Promise<Response>) {
  const response = await send(await authWorker().authorization())
  if (response.status !== 401) return response
  if (!(await recoverFrom401())) return response
  return send(await authWorker().authorization())
}

function mergeHeaders(existing: HeadersInit | undefined, extra: Record<string, string>): Headers {
  const merged = new Headers(existing)
  for (const [name, value] of Object.entries(extra)) merged.set(name, value)
  return merged
}

export const authFetch: typeof fetch = async (input, init) => {
  return withAuth(async (headers) => {
    const url = input instanceof URL ? input.toString() : input
    return ofetch.raw(url, {
      ...init,
      headers: mergeHeaders(init?.headers, headers),
      ignoreResponseError: true,
      retry: 0
    })
  })
}

/**
 * ofetch is the one HTTP engine under the seam: `fetcher` speaks the REST
 * envelope, the Connect transport rides the same `authFetch`. No client
 * calls ofetch directly.
 */
export const fetcher = ofetch.create(
  {
    baseURL: API_BASE_URL,
    retry: 0
  },
  { fetch: authFetch }
)

/**
 * The Connect transport over the seam. In production, point `PUBLIC_RPC_URL`
 * — same parent domain so the requests stay first-party against the Bearer
 * credential the auth worker holds.
 */
export const rpcTransport = createConnectTransport({
  baseUrl: RPC_BASE_URL,
  fetch: authFetch
})
