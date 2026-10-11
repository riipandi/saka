import { create } from '@bufbuild/protobuf'
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider
} from '@tanstack/react-router'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { AuthContext } from '#/hooks/use-auth'
import { setAuthGrants } from '#/libraries/guard/auth-store'
import { UsersView } from '#/routes/(app)/admin/-users-view'
import { UserDetailView } from '#/routes/(app)/admin/users/-user-detail-view'

// The action triggers now sit behind the permission gates, so the file's
// tests run as an administrator holding every action the screens offer.
setAuthGrants({
  roles: ['administrator'],
  permissions: [
    'user:*:read',
    'user:*:list',
    'user:*:create',
    'user:*:update',
    'user:*:delete',
    'user:*:ban',
    'user:*:unban',
    'user:*:assign_role',
    'user:*:grant_permission',
    'session:*:impersonate'
  ]
})
import { AdminResetUserPasswordResponseSchema, PasswordRecoveryService } from '~/codegen/authn_pb'
import type { ListMetadata } from '~/codegen/common_pb'
import {
  BanUserResponseSchema,
  CreateUserResponseSchema,
  GetUserResponseSchema,
  ListUsersResponseSchema,
  UnbanUserResponseSchema,
  UserSchema,
  UserService,
  type User
} from '~/codegen/identity_pb'
import { ListCredentialsResponseSchema, WebAuthnService } from '~/codegen/webauthn_pb'

const LANGDON = create(UserSchema, {
  id: 'usr_langdon',
  username: 'rlangdon',
  email: 'robert@langdon.dev',
  displayName: 'Robert Langdon',
  firstName: 'Robert',
  lastName: 'Langdon',
  createdAt: '2026-09-01T08:00:00Z',
  passwordUpdatedAt: '2026-09-10T08:00:00Z'
})

const VETERI_BANNED = create(UserSchema, {
  id: 'usr_veteri',
  username: 'veteri',
  email: 'veteri@example.com',
  displayName: 'Veteri',
  createdAt: '2026-09-02T08:00:00Z',
  bannedAt: '2026-10-01T08:00:00Z',
  banReason: 'spamming the audit trail'
})

function usersPage(rows: User[], metadata?: Partial<ListMetadata>) {
  return create(ListUsersResponseSchema, {
    users: rows,
    status: 'success',
    message: 'ok',
    metadata: {
      page: 1,
      limit: 20,
      totalPages: 1,
      totalItems: rows.length,
      firstItemIndex: 0,
      lastItemIndex: rows.length,
      ...metadata
    }
  })
}

function withRouter(children: React.ReactNode, initial = '/') {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: [initial] })
  })
  return <RouterProvider router={router} />
}

function authContext(overrides: Record<string, unknown> = {}) {
  return {
    user: LANGDON,
    loggedIn: true,
    isLoading: false,
    login: vi.fn(),
    continueSignIn: vi.fn(),
    completeSignIn: vi.fn(),
    verifyPasskeyLogin: vi.fn(),
    requestOneTimeAccess: vi.fn(),
    exchangeOneTimeToken: vi.fn(),
    stopImpersonation: vi.fn(async () => {}),
    impersonateUser: vi.fn(async () => {}),
    logout: vi.fn(),
    ...overrides
  }
}

/** The dialog overlay intercepts pointer events, so a confirm click inside
 * an open dialog goes through the element itself — the pattern the security
 * tests keep. */
async function confirmInDialog(view: RenderResult, name: string) {
  await expect.element(view.getByRole('alertdialog')).toBeVisible()
  const button = view.getByRole('alertdialog').getByRole('button', { name }).element()
  if (!(button instanceof HTMLElement)) throw new Error(`the ${name} button is not an element`)
  button.click()
}

describe('the users list', () => {
  it('renders the page of accounts with their state badges', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(UserService, {
        listUsers: () => usersPage([LANGDON, VETERI_BANNED])
      })
    })

    const screen = await render(
      withRouter(
        <TransportProvider transport={transport}>
          <QueryClientProvider client={new QueryClient()}>
            <UsersView />
          </QueryClientProvider>
        </TransportProvider>
      )
    )

    await expect.element(screen.getByText('Robert Langdon')).toBeVisible()
    expect(screen.baseElement.textContent).toContain('rlangdon · robert@langdon.dev')
    await expect.element(screen.getByText('Banned')).toBeVisible()
  })

  it('carries the search term and the page on the wire', async () => {
    const seen: Array<{ search?: string; page?: number }> = []
    const transport = createRouterTransport(({ service }) => {
      service(UserService, {
        listUsers: (request) => {
          seen.push({ search: request.search, page: request.page })
          return usersPage([])
        }
      })
    })

    const screen = await render(
      withRouter(
        <TransportProvider transport={transport}>
          <QueryClientProvider client={new QueryClient()}>
            <UsersView page={2} search='langdon' />
          </QueryClientProvider>
        </TransportProvider>
      )
    )

    await expect.element(screen.getByText('No accounts match')).toBeVisible()
    expect(seen[0]).toMatchObject({ search: 'langdon', page: 2 })
  })

  it('creates an account without a credential and hands it to the caller', async () => {
    const created: Array<{ username: string; password?: string }> = []
    const transport = createRouterTransport(({ service }) => {
      service(UserService, {
        listUsers: () => usersPage([]),
        createUser: (request) => {
          created.push({ username: request.username, password: request.password })
          return create(CreateUserResponseSchema, {
            user: LANGDON,
            status: 'success',
            message: 'created'
          })
        }
      })
    })
    const onCreated = vi.fn()

    const screen = await render(
      withRouter(
        <TransportProvider transport={transport}>
          <QueryClientProvider client={new QueryClient()}>
            <UsersView onCreated={onCreated} />
          </QueryClientProvider>
        </TransportProvider>
      )
    )

    await screen.getByRole('button', { name: 'Create user' }).click()
    await expect.element(screen.getByRole('alertdialog')).toBeVisible()
    await screen.getByRole('alertdialog').getByLabelText('Username').fill('sneveu')
    await screen.getByRole('alertdialog').getByLabelText('Email').fill('sophie@neveu.dev')
    await screen.getByRole('alertdialog').getByLabelText('First name').fill('Sophie')
    await screen.getByRole('alertdialog').getByLabelText('Last name').fill('Neveu')
    await confirmInDialog(screen, 'Create user')

    await expect.poll(() => created.length).toBe(1)
    expect(created[0]).toMatchObject({ username: 'sneveu', password: undefined })
    expect(onCreated).toHaveBeenCalledOnce()
  })
})

const transportFor = (options: { resetRefusal?: ConnectError; deleteRefusal?: ConnectError }) =>
  createRouterTransport(({ service }) => {
    service(UserService, {
      getUser: () => create(GetUserResponseSchema, { user: LANGDON, status: 'success' }),
      deleteUser: () => {
        throw options.deleteRefusal ?? new Error('not expected in this test')
      },
      banUser: (request) =>
        create(BanUserResponseSchema, {
          user: LANGDON,
          status: 'success',
          message: request.reason
        })
    })
    service(WebAuthnService, {
      adminListCredentials: () => create(ListCredentialsResponseSchema, { credentials: [] })
    })
    service(PasswordRecoveryService, {
      adminResetUserPassword: () => {
        throw options.resetRefusal ?? new Error('not expected in this test')
      }
    })
  })

const detailHarness = (
  transport: ReturnType<typeof createRouterTransport>,
  contextOverrides: Record<string, unknown> = {}
): Promise<RenderResult> =>
  render(
    withRouter(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={new QueryClient()}>
          <AuthContext.Provider value={authContext(contextOverrides)}>
            <UserDetailView userId='usr_langdon' />
          </AuthContext.Provider>
        </QueryClientProvider>
      </TransportProvider>
    )
  )

describe('the user detail page', () => {
  it('renders the cards and hands the impersonation to the context with its reason', async () => {
    const impersonateUser = vi.fn(async () => {})
    const transport = transportFor({})

    const view = await detailHarness(transport, { impersonateUser })
    await expect.element(view.getByRole('heading', { name: 'Profile' })).toBeVisible()
    await expect.element(view.getByRole('heading', { name: 'Ban' })).toBeVisible()

    await view.getByRole('textbox', { name: 'Reason' }).fill('investigating a session report')
    await view.getByRole('button', { name: 'Start impersonating' }).click()
    expect(impersonateUser).toHaveBeenCalledWith('usr_langdon', 'investigating a session report')
  })

  it('bans with the required reason and no expiry when none is given', async () => {
    const banned: Array<{ reason: string; hasExpiry: boolean }> = []
    const transport = createRouterTransport(({ service }) => {
      service(UserService, {
        getUser: () => create(GetUserResponseSchema, { user: LANGDON, status: 'success' }),
        banUser: (request) => {
          banned.push({ reason: request.reason, hasExpiry: request.expiresAt !== undefined })
          return create(BanUserResponseSchema, {
            user: LANGDON,
            status: 'success',
            message: 'banned'
          })
        }
      })
      service(WebAuthnService, {
        adminListCredentials: () => create(ListCredentialsResponseSchema, { credentials: [] })
      })
      service(PasswordRecoveryService, {
        adminResetUserPassword: () => create(AdminResetUserPasswordResponseSchema, {})
      })
    })

    const view = await detailHarness(transport)

    await view.getByRole('button', { name: 'Ban' }).click()
    await view.getByRole('alertdialog').getByLabelText('Reason').fill('credential stuffing')
    await confirmInDialog(view, 'Ban')

    await expect.poll(() => banned.length).toBe(1)
    expect(banned[0]).toMatchObject({ reason: 'credential stuffing', hasExpiry: false })
  })

  it('surfaces the reset ask refusal verbatim — the cooldown is the service word', async () => {
    const transport = transportFor({
      resetRefusal: new ConnectError('a reset was asked too recently', Code.ResourceExhausted)
    })

    const view = await detailHarness(transport)

    await view.getByRole('button', { name: 'Email a reset' }).click()
    await confirmInDialog(view, 'Email a reset')
    await expect.element(view.getByText('a reset was asked too recently')).toBeVisible()
  })

  it('surfaces the delete-self refusal inside the confirmation', async () => {
    const transport = transportFor({
      deleteRefusal: new ConnectError(
        'You cannot delete the account you are signed in with.',
        Code.FailedPrecondition
      )
    })

    const view = await detailHarness(transport)

    await view.getByRole('button', { name: 'Delete account' }).click()
    await confirmInDialog(view, 'Delete account')
    await expect
      .element(
        view
          .getByRole('alertdialog')
          .getByText('You cannot delete the account you are signed in with.')
      )
      .toBeVisible()
  })

  it('offers the unban to a banned account, not the ban', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(UserService, {
        getUser: () => create(GetUserResponseSchema, { user: VETERI_BANNED, status: 'success' }),
        unbanUser: () =>
          create(UnbanUserResponseSchema, {
            user: VETERI_BANNED,
            status: 'success',
            message: 'lifted'
          })
      })
      service(WebAuthnService, {
        adminListCredentials: () => create(ListCredentialsResponseSchema, { credentials: [] })
      })
      service(PasswordRecoveryService, {
        adminResetUserPassword: () => create(AdminResetUserPasswordResponseSchema, {})
      })
    })

    const view = await detailHarness(transport)

    expect(view.baseElement.textContent).toContain('spamming the audit trail')
    await expect.element(view.getByRole('button', { name: 'Lift ban' })).toBeVisible()
  })
})
