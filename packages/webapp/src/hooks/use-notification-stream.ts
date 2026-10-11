import { createClient } from '@connectrpc/connect'
import { createConnectQueryKey, useTransport } from '@connectrpc/connect-query'
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { NotificationService } from '~/codegen/notification_pb'

/**
 * Reconnect delays the stream tries between attempts, in milliseconds.
 * Exponential with a jitter so a fleet of reconnecting tabs does not
 * stampede the server on the same tick.
 */
const BASE_DELAY_MS = 1000
const MAX_DELAY_MS = 30_000

/**
 * The live tail every signed-in surface shares. Opens
 * `WatchNotifications` over the app's Connect transport, consumes the
 * async iterator, and — on every `notification` event — invalidates the
 * unread count and the inbox queries so whichever screen is mounted
 * refetches. A `ping` is the keepalive and needs nothing.
 *
 * The stream carries no history (the backend's broker has none): after a
 * reconnect the invalidation itself is the catch-up, because the
 * refetched list answers what was created while the socket was down.
 * Disconnects retry with exponential backoff and jitter; the retry loop
 * ends when the component (or session) that asked for the stream goes
 * away. Renders nothing — mount it once in the authenticated layout.
 *
 * `onNotification` rides the same event before the invalidation — the
 * toast the shell shows. The callback is read through a ref, so passing
 * an inline closure does not re-open the stream.
 */
export function useNotificationStream(
  enabled: boolean,
  onNotification?: (notification: {
    id: string
    category: string
    topic: string
    title: string
    body: string
  }) => void
): void {
  const queryClient = useQueryClient()
  const transport = useTransport()
  const generation = useRef(0)
  const listener = useRef(onNotification)
  listener.current = onNotification

  useEffect(() => {
    if (!enabled) return undefined
    generation.current += 1
    const thisGeneration = generation.current

    let closed = false
    let attempt = 0
    let timer: ReturnType<typeof setTimeout> | null = null

    const invalidate = () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: NotificationService.method.unreadCount,
          cardinality: 'finite'
        })
      })
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: NotificationService.method.listNotifications,
          cardinality: 'finite'
        })
      })
    }

    const delay = () => {
      const exponential = Math.min(BASE_DELAY_MS * 2 ** attempt, MAX_DELAY_MS)
      return exponential / 2 + Math.random() * (exponential / 2)
    }

    const open = async () => {
      // One attempt per call: the stream's end (polite or error) is the
      // drop, and the reconnect is the scheduled re-entry — the while
      // form would read `closed` as loop-constant to the linter.
      try {
        const client = createClient(NotificationService, transport)
        const events = client.watchNotifications({})
        for await (const event of events) {
          // A clean event loop resets the backoff — the connection works.
          attempt = 0
          if (event.kind !== 'notification' || !event.notification) continue
          listener.current?.({
            id: event.notification.id,
            category: event.notification.category,
            topic: event.notification.topic ?? '',
            title: event.notification.title,
            body: event.notification.body
          })
          invalidate()
        }
        // The server closed the stream politely; treat it as a drop.
      } catch {
        // The transport refused or the stream errored; retry below.
      }
      if (closed || generation.current !== thisGeneration) return
      attempt += 1
      timer = setTimeout(() => void open(), delay())
    }

    void open()

    return () => {
      closed = true
      if (timer !== null) clearTimeout(timer)
    }
  }, [enabled, queryClient, transport, listener])
}
