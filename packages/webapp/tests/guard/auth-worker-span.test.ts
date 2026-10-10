import type { ReadableSpan } from '@opentelemetry/sdk-trace-web'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { clearAuth } from '#/libraries/guard/auth-store'
import { authWorker } from '#/libraries/guard/auth-worker-client'
import { configureTelemetry, shutdownTelemetry } from '#/libraries/telemetry/telemetry'
import { jsonResponse, tokenJson, userJson } from './auth-connect-mock'

/**
 * The worker joins the trace through the facade: the main-thread wrapper
 * records the operation span and the engine's RPC carries the `traceparent`
 * — proven here with the real main-thread client and a stubbed wire, the
 * same shape the Comlink path ships (the hints ride the call, not mutable
 * transport state).
 */

async function collect(batchMillis = 50): Promise<ReadableSpan[]> {
  const seen: ReadableSpan[] = []
  await configureTelemetry(
    {
      endpoint: 'http://localhost:4318',
      ratio: 0,
      environment: 'development',
      version: 'test',
      batchMillis
    },
    {
      export: (spans, callback) => {
        seen.push(...spans)
        callback({ code: 0 })
      },
      shutdown: () => Promise.resolve(),
      forceFlush: () => Promise.resolve()
    }
  )
  return seen
}

async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 120))
}

describe('the worker joins the trace', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>
  let seen: ReadableSpan[]

  beforeEach(async () => {
    clearAuth()
    seen = await collect()
    fetchMock = vi.fn<typeof fetch>()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(async () => {
    await shutdownTelemetry()
    seen = []
    vi.unstubAllGlobals()
  })

  it('the sign-in operation spans the call and hands the traceparent to the RPC', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ ...tokenJson, user: userJson, status: 'success' }))
    const client = authWorker()

    await client.login({ identity: 'rlangdon', password: 'sophie' })
    await settle()

    const operation = seen.find((span) => span.name === 'auth login')
    if (!operation) throw new Error('no operation span')
    expect(operation.status.code).toBe(0)

    const wireHeaders = new Headers(fetchMock.mock.calls.at(-1)?.[1]?.headers)
    const traceparent = wireHeaders.get('traceparent') ?? ''
    expect(traceparent).toContain(
      `00-${operation.spanContext().traceId}-${operation.spanContext().spanId}-01`
    )
  })

  it('a failed operation ends in error and still carries its context', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ code: 'unauthenticated' }, 401))
    const client = authWorker()

    await expect(client.login({ identity: 'rlangdon', password: 'sophie' })).rejects.toThrow(
      /unauthenticated/i
    )
    await settle()

    const operation = seen.find((span) => span.name === 'auth login')
    if (!operation) throw new Error('no operation span')
    expect(operation.status.code).toBe(2)
  })

  it('the session read spans and the traceparent reaches the GetSession wire', async () => {
    fetchMock.mockImplementation(() =>
      Promise.resolve(jsonResponse({ ...tokenJson, user: userJson, status: 'success' }))
    )
    const client = authWorker()
    await client.login({ identity: 'rlangdon', password: 'sophie' })
    await settle()
    seen.length = 0

    await client.session()
    await settle()

    const operation = seen.find((span) => span.name === 'auth session')
    if (!operation) throw new Error('no operation span')
    const wireHeaders = new Headers(fetchMock.mock.calls.at(-1)?.[1]?.headers)
    expect(wireHeaders.get('traceparent')).toContain(operation.spanContext().traceId)
  })

  it('sign-out spans; the plumbing calls produce no spans of their own', async () => {
    fetchMock.mockImplementation(() =>
      Promise.resolve(jsonResponse({ ...tokenJson, user: userJson, status: 'success' }))
    )
    const client = authWorker()
    await client.login({ identity: 'rlangdon', password: 'sophie' })
    await settle()
    seen.length = 0

    await client.accessToken()
    await client.authorization()
    await settle()

    fetchMock.mockImplementation(() => Promise.resolve(jsonResponse({ status: 'success' })))
    await client.logout()
    await settle()

    expect(seen.map((span) => span.name)).toEqual(['auth logout'])
  })

  it('the off path passes no traceparent and exports no span', async () => {
    await shutdownTelemetry()
    seen = []
    fetchMock.mockResolvedValue(jsonResponse({ ...tokenJson, user: userJson, status: 'success' }))
    const client = authWorker()

    await client.login({ identity: 'rlangdon', password: 'sophie' })
    await settle()

    const wireHeaders = new Headers(fetchMock.mock.calls.at(-1)?.[1]?.headers)
    expect(wireHeaders.get('traceparent')).toBeNull()
    expect(seen).toHaveLength(0)
  })
})
