import { isSpanContextValid, propagation, ROOT_CONTEXT } from '@opentelemetry/api'
import { ExportResultCode } from '@opentelemetry/core'
import type { ReadableSpan, SpanExporter } from '@opentelemetry/sdk-trace-web'
import { afterEach, beforeEach, describe, expect, it } from 'vite-plus/test'
import {
  configureTelemetry,
  samplerFor,
  shutdownTelemetry,
  telemetryActive,
  webTracer
} from '#/libraries/telemetry/telemetry'

const config = {
  endpoint: 'http://localhost:4318',
  ratio: 0,
  environment: 'development',
  version: 'test'
}

/** A stand-in exporter that records what the pipeline hands it and keeps the
 * record across a shutdown — unlike the in-memory exporter, whose shutdown
 * clears its own history. */
function recordingExporter(): SpanExporter & { seen: ReadableSpan[] } {
  const seen: ReadableSpan[] = []
  return {
    seen,
    export: (spans, callback) => {
      seen.push(...spans)
      callback({ code: ExportResultCode.SUCCESS })
    },
    shutdown: () => Promise.resolve(),
    forceFlush: () => Promise.resolve()
  }
}

async function flush(batchMillis: number): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, batchMillis + 50))
}

beforeEach(async () => {
  await shutdownTelemetry()
})

afterEach(async () => {
  await shutdownTelemetry()
})

describe('samplerFor', () => {
  it('samples every trace on a zero ratio — the loud default', () => {
    const decision = samplerFor(0).shouldSample(
      ROOT_CONTEXT,
      '0af7651916cd43dd8448eb211c80319c',
      'span',
      1,
      {},
      []
    )
    expect(decision.decision).toBe(2)
  })

  it('rides the parent under a ratio', () => {
    const sampler = samplerFor(0.25)
    expect(sampler.toString()).toContain('ParentBased')
    expect(sampler.toString()).toContain('0.25')
  })
})

describe('configureTelemetry', () => {
  it('refuses to start on an empty endpoint', async () => {
    await expect(configureTelemetry({ ...config, endpoint: '' })).resolves.toBe(false)
    expect(telemetryActive()).toBe(false)
  })

  it('installs the global tracer and propagator on a configured endpoint', async () => {
    await expect(configureTelemetry(config, recordingExporter())).resolves.toBe(true)
    expect(telemetryActive()).toBe(true)
    expect(propagation.fields()).toContain('traceparent')
  })

  it('is idempotent while the tracer runs', async () => {
    const exporter = recordingExporter()
    await configureTelemetry(config, exporter)
    await expect(configureTelemetry(config, exporter)).resolves.toBe(true)
  })

  it('never exports the span that describes the telemetry endpoint itself', async () => {
    const exporter = recordingExporter()
    await configureTelemetry({ ...config, batchMillis: 10 }, exporter)

    webTracer('test')
      .startSpan('POST /v1/traces', {
        attributes: { 'url.full': `${config.endpoint}/v1/traces?x=1` }
      })
      .end()

    await flush(10)
    expect(exporter.seen).toHaveLength(0)
  })

  it('exports a same-origin span with its query stripped', async () => {
    const exporter = recordingExporter()
    await configureTelemetry({ ...config, batchMillis: 10 }, exporter)

    webTracer('test')
      .startSpan('GET /rpc', {
        attributes: { 'url.full': 'http://localhost:3000/rpc?code=expecto-patronum' }
      })
      .end()

    await flush(10)
    const first = exporter.seen[0]
    if (!first) throw new Error('the exporter saw nothing')
    expect(first.attributes['url.full']).toBe('http://localhost:3000/rpc')
  })

  it('restores the no-op globals after shutdown', async () => {
    await configureTelemetry(config, recordingExporter())
    await shutdownTelemetry()

    const span = webTracer('test').startSpan('after shutdown')
    expect(isSpanContextValid(span.spanContext())).toBe(false)
  })
})
