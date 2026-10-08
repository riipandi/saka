import { afterEach, describe, expect, it, vi } from 'vite-plus/test'
import { authFetch } from '#/libraries/api-client'
import { jsonResponse } from '../guard/auth-connect-mock'

const worker = {
  token: 'access-a' as string | null,
  refreshOutcome: true,
  nextToken: 'access-b' as string | null
}

vi.mock('#/libraries/guard/auth-worker-client', () => ({
  authWorker: () => ({
    authorization: vi.fn<() => Promise<Record<string, string>>>(async () => {
      const headers: Record<string, string> = {}
      if (worker.token) headers.authorization = `Bearer ${worker.token}`
      return headers
    }),
    refresh: vi.fn<() => Promise<boolean>>(async () => {
      if (!worker.refreshOutcome) return false
      worker.token = worker.nextToken
      return true
    }),
    accessToken: vi.fn<() => Promise<string | null>>(async () => worker.token)
  })
}))

vi.mock('#/libraries/guard/auth-store', () => ({
  clearAuth: vi.fn<() => void>()
}))

import { clearAuth } from '#/libraries/guard/auth-store'

describe('authFetch (the shared seam)', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    worker.token = 'access-a'
    worker.refreshOutcome = true
    worker.nextToken = 'access-b'
  })

  it('injects the Bearer the worker holds', async () => {
    const send = stubFetch(jsonResponse({ ok: true }))

    await authFetch('/api/x', { headers: { 'x-trace': 't' } })

    const call = send.mock.calls.at(-1)
    expect(new Headers(call?.[1]?.headers).get('authorization')).toBe('Bearer access-a')
    expect(new Headers(call?.[1]?.headers).get('x-trace')).toBe('t')
  })

  it('goes out headerless for an anonymous caller', async () => {
    worker.token = null
    const send = stubFetch(jsonResponse({ ok: true }))

    await authFetch('/api/configuration')

    const call = send.mock.calls.at(-1)
    expect(new Headers(call?.[1]?.headers).get('authorization')).toBeNull()
  })

  it('replays a 401 once with the refreshed Bearer', async () => {
    const send = stubFetch(
      jsonResponse({ code: 'unauthenticated' }, 401),
      jsonResponse({ ok: true })
    )

    const response = await authFetch('/rpc/x')

    expect(response.status).toBe(200)
    expect(send).toHaveBeenCalledTimes(2)
    const replay = send.mock.calls[1]
    expect(new Headers(replay?.[1]?.headers).get('authorization')).toBe('Bearer access-b')
  })

  it('keeps the original 401 when the refresh cannot save the request', async () => {
    worker.refreshOutcome = false
    worker.token = null
    const send = stubFetch(jsonResponse({ code: 'unauthenticated' }, 401))

    const response = await authFetch('/rpc/x')

    expect(response.status).toBe(401)
    expect(send).toHaveBeenCalledTimes(1)
    expect(clearAuth).toHaveBeenCalledTimes(1)
  })

  it('keeps the session when the pair survives an inconclusive refresh', async () => {
    worker.refreshOutcome = false
    stubFetch(jsonResponse({ code: 'unauthenticated' }, 401))

    await authFetch('/rpc/x')

    expect(clearAuth).not.toHaveBeenCalled()
  })

  it('passes every other response through untouched', async () => {
    const send = stubFetch(jsonResponse({ ok: true }))

    const response = await authFetch('/rpc/x')

    expect(response.status).toBe(200)
    expect(send).toHaveBeenCalledTimes(1)
  })
})

/** authFetch rides ofetch, which calls the global fetch — stand in for it. */
function stubFetch(...responses: Response[]): ReturnType<typeof vi.fn<typeof fetch>> {
  const send = vi.fn<typeof fetch>()
  for (const response of responses) send.mockResolvedValueOnce(response)
  vi.stubGlobal('fetch', send)
  return send
}
