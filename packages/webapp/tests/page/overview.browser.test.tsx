import { create } from '@bufbuild/protobuf'
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider
} from '@tanstack/react-router'
import { describe, expect, it } from 'vite-plus/test'
import { type RenderResult } from 'vitest-browser-react'
import { OverviewSections } from '#/routes/(app)/-overview-sections'
import {
  ApiKeyService,
  ListAPIKeysResponseSchema,
  type ListAPIKeysResponse
} from '~/codegen/api_key_pb'
import {
  AuditLogSchema,
  AuditLogService,
  ListAllResponseSchema,
  ListResponseSchema,
  type ListResponse
} from '~/codegen/auditlog_pb'
import {
  ListSessionsResponseSchema,
  type ListSessionsResponse,
  ListTotpEnrollmentsResponseSchema,
  type ListTotpEnrollmentsResponse,
  MultifactorService,
  SessionSchema,
  SessionService
} from '~/codegen/authn_pb'
import { ListOAuthConnectionsResponseSchema, OAuthSSOService } from '~/codegen/authn_pb'
import { ListOidcClientsResponseSchema, OidcClientService } from '~/codegen/federation_pb'
import {
  ListUserGroupsResponseSchema,
  ListUsersResponseSchema,
  UserGroupService,
  UserService
} from '~/codegen/identity_pb'
import {
  ListNotificationsResponseSchema,
  type ListNotificationsResponse,
  NotificationService
} from '~/codegen/notification_pb'
import {
  ListQueuesResponseSchema,
  ListSchedulerJobsResponseSchema,
  QueueService,
  QueueSummarySchema,
  SchedulerService
} from '~/codegen/system_pb'
import {
  CredentialSchema,
  ListCredentialsResponseSchema,
  type ListCredentialsResponse,
  WebAuthnService
} from '~/codegen/webauthn_pb'
import { ListWebhooksResponseSchema, WebhookService } from '~/codegen/webhook_pb'
import { renderView } from './view-harness'

function withRouter(children: React.ReactNode) {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] })
  })
  return <RouterProvider router={router} />
}

const future = '2099-01-01T00:00:00Z'
const live = (id: string) =>
  create(SessionSchema, {
    id,
    provider: 'password',
    createdAt: '2026-10-01T08:00:00Z',
    expiresAt: future
  })

type Answer<T> = () => T | Promise<T>

interface AdminAnswers {
  dead?: number
  /** Runs once per administration procedure the page calls. */
  onCall?: () => void
}

interface Answers {
  sessions?: Answer<ListSessionsResponse>
  credentials?: Answer<ListCredentialsResponse>
  enrollments?: Answer<ListTotpEnrollmentsResponse>
  unread?: Answer<ListNotificationsResponse>
  keys?: Answer<ListAPIKeysResponse>
  activity?: Answer<ListResponse>
}

async function mount(
  answers: Answers = {},
  opts: { administrator?: boolean; admin?: AdminAnswers } = {}
): Promise<RenderResult> {
  const transport = createRouterTransport(({ service }) => {
    service(SessionService, {
      listSessions:
        answers.sessions ??
        (() =>
          create(ListSessionsResponseSchema, {
            sessions: [
              live('sess_hogwarts'),
              live('sess_gryffindor'),
              create(SessionSchema, {
                id: 'sess_ended',
                expiresAt: future,
                revokedAt: '2026-10-02T08:00:00Z'
              }),
              create(SessionSchema, { id: 'sess_expired', expiresAt: '2020-01-01T00:00:00Z' })
            ]
          }))
    })
    service(WebAuthnService, {
      listCredentials:
        answers.credentials ??
        (() =>
          create(ListCredentialsResponseSchema, {
            credentials: [create(CredentialSchema, { id: 'cred_horcrux' })]
          }))
    })
    service(MultifactorService, {
      listTotpEnrollments:
        answers.enrollments ??
        (() => create(ListTotpEnrollmentsResponseSchema, { enrollments: [] }))
    })
    service(NotificationService, {
      listNotifications:
        answers.unread ??
        (() =>
          create(ListNotificationsResponseSchema, {
            metadata: { page: 1, limit: 1, totalItems: 3 }
          }))
    })
    service(ApiKeyService, {
      listAPIKeys:
        answers.keys ??
        (() =>
          create(ListAPIKeysResponseSchema, { metadata: { page: 1, limit: 1, totalItems: 2 } }))
    })
    const hit =
      <T,>(response: T) =>
      () => {
        opts.admin?.onCall?.()
        return response
      }
    service(UserService, {
      listUsers: hit(
        create(ListUsersResponseSchema, { metadata: { page: 1, limit: 1, totalItems: 41 } })
      )
    })
    service(UserGroupService, {
      listUserGroups: hit(
        create(ListUserGroupsResponseSchema, { metadata: { page: 1, limit: 1, totalItems: 5 } })
      )
    })
    service(QueueService, {
      listQueues: hit(
        create(ListQueuesResponseSchema, {
          queues: [
            create(QueueSummarySchema, {
              name: 'mail',
              pending: 4n,
              dead: BigInt(opts.admin?.dead ?? 0)
            })
          ]
        })
      )
    })
    service(SchedulerService, {
      listJobs: hit(
        create(ListSchedulerJobsResponseSchema, { metadata: { page: 1, limit: 1, totalItems: 6 } })
      )
    })
    service(WebhookService, {
      list: hit(
        create(ListWebhooksResponseSchema, { metadata: { page: 1, limit: 1, totalItems: 2 } })
      )
    })
    service(OidcClientService, {
      listClients: hit(
        create(ListOidcClientsResponseSchema, { metadata: { page: 1, limit: 1, totalItems: 7 } })
      )
    })
    service(OAuthSSOService, {
      listConnections: hit(create(ListOAuthConnectionsResponseSchema, { connections: [] }))
    })
    service(AuditLogService, {
      listAll: hit(
        create(ListAllResponseSchema, {
          logs: [
            create(AuditLogSchema, {
              id: 'log_admin',
              event: 'account_created',
              username: 'langdon'
            })
          ]
        })
      ),
      list:
        answers.activity ??
        (() =>
          create(ListResponseSchema, {
            logs: [
              create(AuditLogSchema, {
                id: 'log_1',
                event: 'sign_in',
                username: 'hermione',
                ipAddress: '10.0.0.9'
              })
            ]
          }))
    })
  })
  return renderView(withRouter(<OverviewSections administrator={opts.administrator ?? false} />), {
    transport
  })
}

describe('the account overview', () => {
  it('counts only the live sessions and reads the totals the lists answer', async () => {
    const screen = await mount()

    await expect.element(screen.getByText('Active sessions')).toBeVisible()
    await expect.element(screen.getByText('2', { exact: true }).first()).toBeVisible()
    await expect.element(screen.getByText('Unread notifications')).toBeVisible()
    await expect.element(screen.getByText('3', { exact: true })).toBeVisible()
    await expect.element(screen.getByText('Signed in')).toBeVisible()
  })

  it('points an account with no second sign-in method at Security', async () => {
    const screen = await mount({
      credentials: () => create(ListCredentialsResponseSchema, { credentials: [] })
    })

    await expect.element(screen.getByText('None yet — add one in Security.').first()).toBeVisible()
  })

  it('leaves out a tile whose list failed and keeps the others', async () => {
    const screen = await mount({
      keys: () => {
        throw new ConnectError('refused', Code.PermissionDenied)
      }
    })

    await expect.element(screen.getByText('Unread notifications')).toBeVisible()
    expect(screen.baseElement.textContent).not.toContain('API keys')
  })

  it('shows a skeleton, not a zero, while a list is still answering', async () => {
    const screen = await mount({ unread: () => new Promise<ListNotificationsResponse>(() => {}) })

    await expect.element(screen.getByTestId('tile-skeleton').first()).toBeInTheDocument()
    expect(screen.baseElement.textContent).not.toContain('Unread notifications0')
  })
})

describe('the administrator overview', () => {
  it('reads the console counts above the account facts', async () => {
    const screen = await mount({}, { administrator: true })

    await expect.element(screen.getByText('Administration')).toBeVisible()
    await expect.element(screen.getByText('Your account')).toBeVisible()
    await expect.element(screen.getByText('41', { exact: true })).toBeVisible()
    await expect.element(screen.getByText('Account created')).toBeVisible()
  })

  it('marks a queue that holds dead tasks', async () => {
    const screen = await mount({}, { administrator: true, admin: { dead: 3 } })

    await expect.element(screen.getByText('Needs attention')).toBeVisible()
  })

  it('leaves the warning out when no task is dead', async () => {
    const screen = await mount({}, { administrator: true })

    await expect.element(screen.getByText('Dead tasks')).toBeVisible()
    expect(screen.baseElement.textContent).not.toContain('Needs attention')
  })

  it('never asks a non-administrator for an administration list', async () => {
    let calls = 0
    const screen = await mount({}, { administrator: false, admin: { onCall: () => (calls += 1) } })

    await expect.element(screen.getByText('Active sessions')).toBeVisible()
    expect(screen.baseElement.textContent).not.toContain('Administration')
    expect(calls).toBe(0)
  })
})
