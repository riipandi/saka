import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { authRetryFetch } from '#/libraries/api-client'
import { jsonResponse, stringUrl } from './auth-connect-mock'

vi.mock('#/libraries/guard/auth-worker-client', () => ({
  authWorker: () => ({
    refresh: vi.fn<() => Promise<boolean>>(async () => mockRefreshed),
    accessToken: vi.fn<() => Promise<string | null>>(async () =>
      mockRefreshed ? 'access-b' : null
    )
  })
}))

let mockRefreshed = false

describe("auth retry fetch (the RPC transport's silent refresh)", () => {
  beforeEach(() => {
    mockRefreshed = false
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('replays a 401 once with the refreshed Bearer', async () => {
    mockRefreshed = true
    const base = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse({ code: 'unauthenticated' }, 401))
      .mockResolvedValueOnce(jsonResponse({ ok: true }))
    const wrapped = authRetryFetch(base)

    const response = await wrapped('/rpc/x', { headers: { authorization: 'Bearer access-a' } })

    expect(response.status).toBe(200)
    expect(base).toHaveBeenCalledTimes(2)
    const replay = base.mock.calls[1]
    expect(new Headers(replay?.[1]?.headers).get('authorization')).toBe('Bearer access-b')
    expect(stringUrl(replay?.[0])).toBe('/rpc/x')
  })

  it('keeps the original 401 when the refresh cannot save the request', async () => {
    mockRefreshed = false
    const base = vi
      .fn<typeof fetch>()
      .mockResolvedValue(jsonResponse({ code: 'unauthenticated' }, 401))
    const wrapped = authRetryFetch(base)

    const response = await wrapped('/rpc/x')

    expect(response.status).toBe(401)
    expect(base).toHaveBeenCalledTimes(1)
  })

  it('passes every other response through untouched', async () => {
    const base = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse({ ok: true }))
    const wrapped = authRetryFetch(base)

    await wrapped('/rpc/x')

    expect(base).toHaveBeenCalledTimes(1)
  })
})
