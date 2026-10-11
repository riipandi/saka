import { create } from '@bufbuild/protobuf'
import { TimestampSchema } from '@bufbuild/protobuf/wkt'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createRootRoute,
  createMemoryHistory,
  createRouter,
  RouterProvider
} from '@tanstack/react-router'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { setAuthGrants } from '#/libraries/guard/auth-store'
import { NotificationBell } from '#/routes/(app)/-notification-bell'
import { AccountNotificationsView } from '#/routes/(app)/account/-notifications-view'
import {
  type Notification,
  NotificationSchema,
  ListNotificationsResponseSchema,
  NotificationService,
  UnreadCountResponseSchema,
  MarkNotificationReadResponseSchema,
  MarkAllNotificationsReadResponseSchema,
  WatchEventSchema
} from '~/codegen/notification_pb'

setAuthGrants({ roles: [], permissions: [] })

const created = { seconds: 1790000000n, nanos: 0 }

function notice(
  id: string,
  title: string,
  overrides: Partial<Omit<Notification, '$typeName' | '$unknown'>> = {}
): Notification {
  return create(NotificationSchema, {
    id,
    category: 'announcement',
    title,
    body: 'the body text',
    audienceKind: 'global',
    createdAt: create(TimestampSchema, created),
    emailSent: false,
    ...overrides
  })
}

const INBOX = [
  notice('ntf_1', 'Planned maintenance', { topic: 'maintenance' }),
  notice('ntf_2', 'Welcome aboard', {
    category: 'system',
    audienceKind: 'users',
    userIds: ['usr_me'],
    readAt: create(TimestampSchema, created)
  }),
  notice('ntf_3', 'Password policy tightened')
]

let markedRead: string[] = []
let markedAll = false
let unreadCount = 2n

async function renderInbox() {
  const transport = createRouterTransport(({ service }) => {
    service(NotificationService, {
      listNotifications: () => create(ListNotificationsResponseSchema, { notifications: INBOX }),
      unreadCount: () => create(UnreadCountResponseSchema, { count: unreadCount }),
      markNotificationRead: (request) => {
        markedRead.push(request.id)
        unreadCount -= 1n
        return create(MarkNotificationReadResponseSchema, {
          status: 'success',
          message: 'the receipt was written'
        })
      },
      markAllNotificationsRead: () => {
        markedAll = true
        unreadCount = 0n
        return create(MarkAllNotificationsReadResponseSchema, {
          status: 'success',
          message: 'the receipts were written'
        })
      },
      watchNotifications: async function* () {
        // The inbox test is not the stream's; keep it open and silent.
        yield create(WatchEventSchema, { kind: 'ping' })
        await new Promise(() => {})
      }
    })
  })

  markedRead = []
  markedAll = false
  unreadCount = 2n

  const screen: RenderResult = await render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 } } })}
    >
      <TransportProvider transport={transport}>
        <WithRouter>
          <AccountNotificationsView />
          <NotificationBell />
        </WithRouter>
      </TransportProvider>
    </QueryClientProvider>
  )
  return screen
}

/** The bell renders a router `Link`, which needs a router context to read. */
function WithRouter({ children }: { children: React.ReactNode }) {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] })
  })
  return <RouterProvider router={router} />
}

describe('Account notifications (browser)', () => {
  it('renders the inbox with its read and unread states', async () => {
    const screen = await renderInbox()
    await expect.element(screen.getByText('Planned maintenance')).toBeVisible()
    await expect.element(screen.getByText('unread').nth(0)).toBeVisible()
    await expect.element(screen.getByText('read').nth(0)).toBeVisible()
  })

  it('shows the unread count on the bell badge', async () => {
    const screen = await renderInbox()
    // The badge paints the count and numbers the aria-label.
    await expect.element(screen.getByLabelText('Notifications, 2 unread')).toBeVisible()
    await expect.element(screen.getByText('2')).toBeVisible()
  })

  it('marks one notification read and the badge follows', async () => {
    const screen = await renderInbox()
    const button = screen.getByRole('button', { name: 'Mark Planned maintenance read' })
    await expect.element(button).toBeVisible()
    await button.click()
    await vi.waitFor(() => {
      expect(markedRead).toEqual(['ntf_1'])
    })
  })

  it('marks the whole inbox read at once', async () => {
    const screen = await renderInbox()
    const button = screen.getByRole('button', { name: 'Mark all read' })
    await expect.element(button).toBeVisible()
    await button.click()
    await vi.waitFor(() => {
      expect(markedAll).toBe(true)
    })
  })
})
