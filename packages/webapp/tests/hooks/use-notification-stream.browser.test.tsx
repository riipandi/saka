import { create } from '@bufbuild/protobuf'
import { TimestampSchema } from '@bufbuild/protobuf/wkt'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider, useQuery } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it } from 'vite-plus/test'
import { renderHook } from 'vitest-browser-react'
import { useNotificationStream } from '#/hooks/use-notification-stream'
import { setAuthGrants } from '#/libraries/guard/auth-store'
import {
  NotificationSchema,
  NotificationService,
  UnreadCountResponseSchema,
  WatchEventSchema,
  type WatchEvent
} from '~/codegen/notification_pb'

setAuthGrants({ roles: [], permissions: [] })

const notice = create(NotificationSchema, {
  id: 'ntf_1',
  category: 'announcement',
  title: 'Hello',
  body: 'the body',
  audienceKind: 'global',
  createdAt: create(TimestampSchema, { seconds: 1790000000n, nanos: 0 }),
  emailSent: false
})

/** Each transport read of the unread count is one tick of the probe. */
let unreadReads = 0

function streamTransport(stream: () => AsyncGenerator<WatchEvent>) {
  return createRouterTransport(({ service }) => {
    service(NotificationService, {
      watchNotifications: stream,
      unreadCount: () => {
        unreadReads += 1
        return create(UnreadCountResponseSchema, { count: BigInt(unreadReads) })
      }
    })
  })
}

function Probe() {
  // An active observer of the invalidated key — without it, invalidate
  // marks stale and refetches nothing.
  const unread = useQuery(NotificationService.method.unreadCount, {}, { retry: false })
  return <p data-testid='unread'>{String(unread.data?.count ?? 'none')}</p>
}

function harness(transport: ReturnType<typeof streamTransport>) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 } } })
  return {
    client,
    wrapper: ({ children }: { children: React.ReactNode }) => (
      <QueryClientProvider client={client}>
        <TransportProvider transport={transport}>
          {children}
          <Probe />
        </TransportProvider>
      </QueryClientProvider>
    )
  }
}

/** The hoisted mock stream: one event, then an idle keepalive — nothing
 * consumes it when the hook under test is disabled. */
function hoistedDisabledStream(): AsyncGenerator<WatchEvent> {
  return (async function* () {
    yield create(WatchEventSchema, { kind: 'notification', notification: notice })
    await new Promise(() => {})
  })()
}

describe('useNotificationStream', () => {
  it('consumes notification events and invalidates the unread count', async () => {
    const events = [create(WatchEventSchema, { kind: 'notification', notification: notice })]
    async function* stream(): AsyncGenerator<WatchEvent> {
      for (const event of events) yield event
      // The stream stays logically open; the pending promise is the mock's
      // idle keepalive, so the hook's reconnect loop never fires here.
      await new Promise(() => {})
    }
    unreadReads = 0

    const value = harness(streamTransport(stream))
    const mounted = await renderHook(() => useNotificationStream(true), {
      wrapper: value.wrapper
    })

    // One read from the observer mounting, one from the invalidation's
    // refetch — the stream event is the only thing that can cause the second.
    await expect.poll(() => unreadReads, { timeout: 5000 }).toBeGreaterThanOrEqual(2)
    void mounted.unmount()
  })

  it('does nothing while disabled', async () => {
    unreadReads = 0
    const value = harness(streamTransport(() => hoistedDisabledStream()))
    const { unmount } = await renderHook(() => useNotificationStream(false), {
      wrapper: value.wrapper
    })
    // One read: the observer's own mount. The stream is off, so no more.
    await new Promise((resolve) => setTimeout(resolve, 400))
    expect(unreadReads).toBe(1)
    void unmount()
  })
})
