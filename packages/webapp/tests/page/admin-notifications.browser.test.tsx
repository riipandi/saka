import { create } from '@bufbuild/protobuf'
import { TimestampSchema } from '@bufbuild/protobuf/wkt'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { setAuthGrants } from '#/libraries/guard/auth-store'
import { NotificationsView } from '#/routes/(app)/admin/-notifications-view'
import {
  ListUsersResponseSchema,
  ListUserGroupsResponseSchema,
  UserService,
  UserGroupService
} from '~/codegen/identity_pb'
import {
  type Notification,
  NotificationSchema,
  type WatchEvent,
  WatchEventSchema,
  ListAllNotificationsResponseSchema,
  ListNotificationsResponseSchema,
  NotificationService,
  UnreadCountResponseSchema,
  CreateNotificationResponseSchema,
  CancelNotificationResponseSchema
} from '~/codegen/notification_pb'

const created = { seconds: 1790000000n, nanos: 0 }

setAuthGrants({
  roles: ['administrator'],
  permissions: [
    'notification:*:list',
    'notification:*:create',
    'notification:*:cancel',
    'user:*:list',
    'user_group:*:list'
  ]
})

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

const LEDGER = [
  notice('ntf_1', 'Planned maintenance', { topic: 'maintenance' }),
  notice('ntf_2', 'Password policy tightened', { emailSent: true }),
  notice('ntf_3', 'Old announcement', {
    cancelledAt: create(TimestampSchema, created)
  }),
  notice('ntf_4', 'Welcome', { category: 'system', audienceKind: 'users', userIds: ['usr_9'] })
]

let published: Array<Record<string, unknown>> = []
let cancelledId: string | undefined

async function renderView(ledger: Notification[] = LEDGER) {
  const transport = createRouterTransport(({ service }) => {
    service(NotificationService, {
      listAllNotifications: () =>
        create(ListAllNotificationsResponseSchema, { notifications: ledger }),
      createNotification: (request) => {
        published.push({ ...request })
        return create(CreateNotificationResponseSchema, {
          notification: notice('ntf_new', request.title, {
            audienceKind: request.audienceKind ?? 'global'
          }),
          status: 'success',
          message: 'the notification went live'
        })
      },
      cancelNotification: (request) => {
        cancelledId = request.id
        return create(CancelNotificationResponseSchema, {
          status: 'success',
          message: 'the notification was withdrawn'
        })
      },
      listNotifications: () =>
        create(ListNotificationsResponseSchema, { notifications: ledger.slice(0, 2) }),
      unreadCount: () => create(UnreadCountResponseSchema, { count: 2n }),
      watchNotifications: async function* () {
        // The probe's mock: one event then the stream ends; the hook's
        // reconnect loop is not this test's subject.
        const event: WatchEvent = create(WatchEventSchema, {
          kind: 'notification',
          notification: ledger[0]
        })
        yield event
      }
    })
    service(UserService, {
      listUsers: () =>
        create(ListUsersResponseSchema, {
          users: [
            {
              id: 'usr_9',
              username: 'hermione',
              email: 'hermione@example.com',
              displayName: 'Hermione'
            },
            { id: 'usr_10', username: 'robert', email: 'robert@example.com', displayName: '' }
          ]
        })
    })
    service(UserGroupService, {
      listUserGroups: () =>
        create(ListUserGroupsResponseSchema, {
          groups: [
            { id: 'ugrp_1', name: 'editors', displayName: 'Editors' },
            { id: 'ugrp_2', name: 'moderators', displayName: '' }
          ]
        })
    })
  })

  published = []
  cancelledId = undefined

  const screen: RenderResult = await render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transport}>
        <NotificationsView />
      </TransportProvider>
    </QueryClientProvider>
  )
  return screen
}

describe('Admin notifications (browser)', () => {
  it('lists the ledger with its state badges', async () => {
    const screen = await renderView()
    await expect.element(screen.getByText('Planned maintenance')).toBeVisible()
    await expect.element(screen.getByText('cancelled').nth(0)).toBeVisible()
    await expect.element(screen.getByText(/emailed/).nth(0)).toBeVisible()
  })

  it('publishes a global announcement through the dialog', async () => {
    const screen = await renderView()
    await screen.getByRole('button', { name: 'New broadcast' }).click()
    const dialog = screen.getByRole('dialog')
    await dialog.getByLabelText('Title').fill('Spring release')
    await dialog.getByLabelText('Body').fill('The spring release is live.')
    const publish = dialog.getByRole('button', { name: 'Publish' }).element()
    if (!(publish instanceof HTMLElement)) throw new Error('the publish button is not an element')
    publish.click()
    await vi.waitFor(() => {
      expect(published).toEqual([
        expect.objectContaining({
          title: 'Spring release',
          category: 'announcement',
          audienceKind: 'global'
        })
      ])
    })
  })

  it('names accounts for a system notice and publishes them', async () => {
    const screen = await renderView()
    await screen.getByRole('button', { name: 'New broadcast' }).click()
    const dialog = screen.getByRole('dialog')
    const categoryTrigger = dialog.getByLabelText('Category').element()
    if (!(categoryTrigger instanceof HTMLElement))
      throw new Error('the category trigger is not an element')
    categoryTrigger.click()
    await screen.getByRole('option', { name: 'System notice' }).click()
    // A system notice preselects the accounts audience; name one account.
    // The Checkbox renders its own labelled element beside the native input.
    const hermione = dialog.getByRole('checkbox', {
      name: 'Hermione (hermione@example.com)'
    })
    const box = hermione.element()
    if (!(box instanceof HTMLElement)) throw new Error('the account row is not an element')
    box.click()
    await dialog.getByLabelText('Title').fill('Your account was flagged')
    await dialog.getByLabelText('Body').fill('Please review the policy.')
    const publish = dialog.getByRole('button', { name: 'Publish' }).element()
    if (!(publish instanceof HTMLElement)) throw new Error('the publish button is not an element')
    publish.click()
    await vi.waitFor(() => {
      expect(published).toEqual([
        expect.objectContaining({
          category: 'system',
          audienceKind: 'users',
          userIds: ['usr_9']
        })
      ])
    })
  })

  it('withdraws a live notice after the confirmation', async () => {
    const screen = await renderView()
    await expect
      .element(screen.getByRole('button', { name: 'Cancel Planned maintenance' }))
      .toBeVisible()
    await screen.getByRole('button', { name: 'Cancel Planned maintenance' }).click()
    const scope = screen.getByRole('alertdialog')
    await expect.element(scope).toBeVisible()
    const confirm = scope.getByRole('button', { name: 'Cancel notification' }).element()
    if (!(confirm instanceof HTMLElement)) throw new Error('the confirm button is not an element')
    confirm.click()
    await vi.waitFor(() => {
      expect(cancelledId).toBe('ntf_1')
    })
  })
})
