import type { ReadableSpan } from '@opentelemetry/sdk-trace-web'
import { describe, expect, it } from 'vite-plus/test'
import { wireNavigationSpans } from '#/libraries/telemetry/navigation'
import { configureTelemetry, shutdownTelemetry } from '#/libraries/telemetry/telemetry'

interface NavEvent {
  type: 'onBeforeLoad' | 'onLoad' | 'onResolved'
  toLocation: { pathname: string }
}

interface FakeRouter {
  router: Parameters<typeof wireNavigationSpans>[0]
  emit: (
    type: NavEvent['type'],
    pathname: string,
    matches?: Array<{ routeId?: string; status?: string }>
  ) => void
  unsubscribe: () => void
}

function fakeRouter(): FakeRouter {
  const listeners = new Map<string, Array<(event: NavEvent) => void>>()
  const router: Parameters<typeof wireNavigationSpans>[0] = {
    state: { matches: [] },
    subscribe: (eventType, fn) => {
      const existing = listeners.get(eventType) ?? []
      existing.push(fn)
      listeners.set(eventType, existing)
      return () => {
        listeners.set(
          eventType,
          (listeners.get(eventType) ?? []).filter((registered) => registered !== fn)
        )
      }
    }
  }
  return {
    router,
    emit: (type, pathname, matches) => {
      if (matches) router.state.matches = matches
      for (const fn of listeners.get(type) ?? []) fn({ type, toLocation: { pathname } })
    },
    unsubscribe: () => listeners.clear()
  }
}

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

// A failing test's own cleanup never ran would leave the tracer's singleton
// guard holding the previous provider for every later test in this file —
// the reset here is what keeps the tests independent of each other's fate.
import { afterEach } from 'vite-plus/test'
afterEach(async () => {
  await shutdownTelemetry()
})

describe('navigation spans', () => {
  it('opens on before-load, renames to the route id, and closes on resolve', async () => {
    const seen = await collect()
    const fake = fakeRouter()
    const unwire = wireNavigationSpans(fake.router)

    // A span is exported when it ends, so the observable start fact is the
    // attribute the exporter carries: the navigation's own path.
    fake.emit('onBeforeLoad', '/settings')
    fake.emit('onResolved', '/settings', [
      { routeId: '/(app)/settings' },
      { routeId: '/(app)/settings/' }
    ])
    await settle()

    const span = seen[0]
    if (!span) throw new Error('no span')
    expect(span.name).toBe('nav /(app)/settings/')
    expect(span.attributes['url.path']).toBe('/settings')

    unwire()
    fake.unsubscribe()
    await shutdownTelemetry()
  })

  it('marks an errored load and keeps the span open until resolve', async () => {
    const seen = await collect()
    const fake = fakeRouter()
    const unwire = wireNavigationSpans(fake.router)

    fake.emit('onBeforeLoad', '/account/audit')
    fake.emit('onLoad', '/account/audit', [{ routeId: '/(app)/account/audit', status: 'error' }])
    fake.emit('onResolved', '/account/audit', [
      { routeId: '/(app)/account/audit', status: 'error' }
    ])
    await settle()

    const span = seen[0]
    if (!span) throw new Error('no span')
    expect(span.name).toBe('nav /(app)/account/audit')
    expect(span.status.code).toBe(2)

    unwire()
    fake.unsubscribe()
    await shutdownTelemetry()
  })

  it('ends a superseded navigation silently when the next one starts', async () => {
    const seen = await collect()
    const fake = fakeRouter()
    const unwire = wireNavigationSpans(fake.router)

    fake.emit('onBeforeLoad', '/login')
    fake.emit('onBeforeLoad', '/overview')
    fake.emit('onResolved', '/overview', [{ routeId: '/(app)/overview' }])
    await settle()

    expect(seen).toHaveLength(2)
    expect(seen[1]?.name).toBe('nav /(app)/overview')

    unwire()
    fake.unsubscribe()
    await shutdownTelemetry()
  })

  it('unsubscribes cleanly — nothing fires after the wire is cut', async () => {
    const seen = await collect()
    const fake = fakeRouter()
    const unwire = wireNavigationSpans(fake.router)
    unwire()

    fake.emit('onBeforeLoad', '/settings')
    fake.emit('onResolved', '/settings', [])
    await settle()
    expect(seen).toHaveLength(0)

    fake.unsubscribe()
    await shutdownTelemetry()
  })
})
