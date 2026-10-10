import { SpanKind, SpanStatusCode } from '@opentelemetry/api'
import { resourceFromAttributes } from '@opentelemetry/resources'
import type { ReadableSpan } from '@opentelemetry/sdk-trace-web'
import { describe, expect, it } from 'vite-plus/test'
import { createRedactingExporter, redactSpan, stripQuery } from '#/libraries/telemetry/-redaction'

const OWN = 'http://localhost:3000'
const TEST_RESOURCE = resourceFromAttributes({})

function span(overrides?: Partial<ReadableSpan>): ReadableSpan {
  return {
    name: 'GET /rpc',
    kind: SpanKind.CLIENT,
    spanContext: () => ({
      traceId: '0af7651916cd43dd8448eb211c80319c',
      spanId: 'b7ad6b7169203331',
      traceFlags: 1,
      isRemote: false,
      traceState: undefined
    }),
    startTime: [1, 0],
    endTime: [1, 1],
    duration: [0, 1],
    ended: false,
    status: { code: SpanStatusCode.UNSET },
    attributes: {},
    links: [],
    events: [],
    droppedAttributesCount: 0,
    droppedEventsCount: 0,
    droppedLinksCount: 0,
    resource: TEST_RESOURCE,
    instrumentationScope: { name: 'test' },
    ...overrides
  }
}

describe('stripQuery', () => {
  it('cuts at the first query or fragment marker', () => {
    expect(stripQuery('http://localhost:3000/verify-email?code=abc123&next=/overview')).toBe(
      'http://localhost:3000/verify-email'
    )
    expect(stripQuery('http://localhost:3000/login#reset-token')).toBe(
      'http://localhost:3000/login'
    )
  })

  it('keeps a bare URL untouched', () => {
    expect(stripQuery('http://localhost:3000/settings')).toBe('http://localhost:3000/settings')
  })
})

describe('redactSpan', () => {
  it('strips the query off the URL facts', () => {
    const redacted = redactSpan(
      span({
        attributes: {
          'url.full': 'http://localhost:3000/verify-email?code=expecto-patronum',
          'url.path': '/verify-email?code=expecto-patronum'
        }
      })
    )
    expect(redacted.attributes['url.full']).toBe('http://localhost:3000/verify-email')
    expect(redacted.attributes['url.path']).toBe('/verify-email')
  })

  it('drops the query and fragment attributes entirely', () => {
    const redacted = redactSpan(
      span({
        attributes: {
          'url.query': 'code=expecto-patronum',
          'url.fragment': 'reset-token',
          'url.full': 'http://localhost:3000/login?code=expecto-patronum#reset-token'
        }
      })
    )
    expect(redacted.attributes).not.toHaveProperty('url.query')
    expect(redacted.attributes).not.toHaveProperty('url.fragment')
    expect(redacted.attributes['url.full']).toBe('http://localhost:3000/login')
  })

  it('returns a span without URL facts untouched', () => {
    const original = span({ name: 'nav' })
    expect(redactSpan(original)).toBe(original)
  })
})

function exporterHarness(overrides?: Partial<Parameters<typeof createRedactingExporter>[1]>) {
  const exported: ReadableSpan[][] = []
  const next = {
    export: (spans: ReadableSpan[], callback: (result: { code: number }) => void) => {
      exported.push(spans)
      callback({ code: 0 })
    },
    shutdown: () => Promise.resolve(),
    forceFlush: () => Promise.resolve()
  }
  const rules = {
    ownOrigin: OWN,
    telemetryEndpoint: 'http://localhost:4318/v1/traces',
    ...overrides
  }
  return { exported, subject: createRedactingExporter(next, rules) }
}

describe('createRedactingExporter', () => {
  const exporter = exporterHarness

  it('strips the query before the next exporter sees the span', async () => {
    const { exported, subject } = exporter()
    await new Promise<void>((resolve) =>
      subject.export(
        [span({ attributes: { 'url.full': `${OWN}/login?code=expecto-patronum` } })],
        () => resolve()
      )
    )
    const batch = exported[0]
    const kept = batch?.[0]
    if (!kept) throw new Error('the next exporter saw nothing')
    expect(kept.attributes['url.full']).toBe(`${OWN}/login`)
  })

  it('drops the span that describes the telemetry endpoint itself', async () => {
    const { exported, subject } = exporter()
    let results = 0
    await new Promise<void>((resolve) =>
      subject.export(
        [span({ attributes: { 'url.full': 'http://localhost:4318/v1/traces' } })],
        () => {
          results += 1
          resolve()
        }
      )
    )
    expect(exported).toHaveLength(0)
    expect(results).toBe(1)
  })

  it('drops a span aimed at a third-party origin', async () => {
    const { exported, subject } = exporter()
    await new Promise<void>((resolve) =>
      subject.export(
        [span({ attributes: { 'url.full': 'https://analytics.example.com/collect' } })],
        () => resolve()
      )
    )
    expect(exported).toHaveLength(0)
  })

  it('keeps a same-origin span with no URL facts', async () => {
    const { exported, subject } = exporter()
    await new Promise<void>((resolve) =>
      subject.export([span({ name: 'nav /settings' })], () => resolve())
    )
    expect(exported).toHaveLength(1)
  })

  it('answers success when every span was dropped, never calling the next exporter', async () => {
    const { exported, subject } = exporter()
    const code = await new Promise<number>((resolve) =>
      subject.export(
        [
          span({ attributes: { 'url.full': 'http://localhost:4318/v1/traces' } }),
          span({ attributes: { 'url.full': 'https://analytics.example.com/collect' } })
        ],
        (result) => resolve(result.code)
      )
    )
    expect(code).toBe(0)
    expect(exported).toHaveLength(0)
  })
})
