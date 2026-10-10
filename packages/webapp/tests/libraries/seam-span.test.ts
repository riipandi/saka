import { SpanKind } from '@opentelemetry/api'
import type { ReadableSpan } from '@opentelemetry/sdk-trace-web'
import { afterEach, describe, expect, it, vi } from 'vite-plus/test'
import { authFetch } from '#/libraries/api-client'
import { configureTelemetry, shutdownTelemetry } from '#/libraries/telemetry/telemetry'
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

vi.mock('#/libraries/device-fingerprint', () => ({
  deviceHeaders: async () => ({ 'x-device-fingerprint': 'fp-test', 'user-agent': 'ua-test' })
}))

import { clearAuth } from '#/libraries/guard/auth-store'

/** Flush waits past the batch's scheduled delay before the spans are read. */
async function drainSpans(batchMillis = 50): Promise<ReadableSpan[]> {
  await new Promise((resolve) => setTimeout(resolve, batchMillis + 100))
  return exported
}

let exported: ReadableSpan[] = []

async function withTracer(): Promise<void> {
  exported = []
  await configureTelemetry(
    {
      endpoint: 'http://localhost:4318',
      ratio: 0,
      environment: 'development',
      version: 'test',
      batchMillis: 50
    },
    {
      export: (spans, callback) => {
        exported.push(...spans)
        callback({ code: 0 })
      },
      shutdown: () => Promise.resolve(),
      forceFlush: () => Promise.resolve()
    }
  )
}

describe('the seam spans (authFetch under the tracer)', () => {
  afterEach(async () => {
    await shutdownTelemetry()
    exported = []
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    worker.token = 'access-a'
    worker.refreshOutcome = true
    worker.nextToken = 'access-b'
  })

  it('opens a client span with the semconv facts and the traceparent', async () => {
    await withTracer()
    const send = stubFetch(jsonResponse({ ok: true }))

    await authFetch('/rpc/saka.identity.v1.UserService/GetCurrentUser', { method: 'POST' })

    const [span] = await drainSpans()
    if (!span) throw new Error('no span reached the exporter')
    expect(span.name).toBe('HTTP POST')
    expect(span.kind).toBe(SpanKind.CLIENT)
    expect(span.attributes['http.request.method']).toBe('POST')
    expect(span.attributes['url.path']).toBe('/rpc/saka.identity.v1.UserService/GetCurrentUser')
    expect(String(span.attributes['url.full'])).toContain('http://localhost:3000/rpc/')

    const sent = new Headers(send.mock.calls.at(-1)?.[1]?.headers)
    const traceparent = sent.get('traceparent') ?? ''
    expect(traceparent).toContain(
      `00-${span.spanContext().traceId}-${span.spanContext().spanId}-01`
    )
  })

  it('marks the span an error on a refusal status', async () => {
    await withTracer()
    stubFetch(jsonResponse({ code: 'not_found' }, 404))

    await authFetch('/rpc/x')

    const [span] = await drainSpans()
    if (!span) throw new Error('no span reached the exporter')
    expect(span.attributes['http.response.status_code']).toBe(404)
    expect(span.status.code).toBe(2)
    expect(span.status.message).toBe('HTTP 404')
  })

  it('stays unset on a 2xx answer', async () => {
    await withTracer()
    stubFetch(jsonResponse({ ok: true }))

    await authFetch('/api/configuration')

    const [span] = await drainSpans()
    if (!span) throw new Error('no span reached the exporter')
    expect(span.status.code).toBe(0)
  })

  it('records the network failure and rethrows', async () => {
    await withTracer()
    const send = vi.fn<typeof fetch>().mockRejectedValue(new TypeError('failed to fetch'))
    vi.stubGlobal('fetch', send)

    await expect(authFetch('/rpc/x')).rejects.toThrow('failed to fetch')

    const [span] = await drainSpans()
    if (!span) throw new Error('no span reached the exporter')
    expect(span.status.code).toBe(2)
    expect(span.events.some((event) => event.name === 'exception')).toBe(true)
  })

  it('gives the 401 replay its own span', async () => {
    await withTracer()
    const send = stubFetch(
      jsonResponse({ code: 'unauthenticated' }, 401),
      jsonResponse({ ok: true })
    )

    await authFetch('/rpc/x')

    expect(send).toHaveBeenCalledTimes(2)
    const spans = await drainSpans()
    expect(spans).toHaveLength(2)
    expect(spans.every((span) => span.attributes['http.response.status_code'] !== undefined)).toBe(
      true
    )
  })

  it('sends no traceparent and costs no span while the tracer is off', async () => {
    const send = stubFetch(jsonResponse({ ok: true }))

    await authFetch('/api/x')

    const headers = new Headers(send.mock.calls.at(-1)?.[1]?.headers)
    expect(headers.get('traceparent')).toBeNull()
    expect(await drainSpans()).toHaveLength(0)
  })

  it('never touches the session behavior the tests above pin', async () => {
    await withTracer()
    worker.refreshOutcome = false
    worker.token = null
    stubFetch(jsonResponse({ code: 'unauthenticated' }, 401))

    await authFetch('/rpc/x')

    expect(clearAuth).toHaveBeenCalledTimes(1)
  })
})

/** authFetch rides the global fetch — stand in for it. */
function stubFetch(...responses: Response[]): ReturnType<typeof vi.fn<typeof fetch>> {
  const send = vi.fn<typeof fetch>()
  for (const response of responses) send.mockResolvedValueOnce(response)
  vi.stubGlobal('fetch', send)
  return send
}
