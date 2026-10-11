import { create } from '@bufbuild/protobuf'
import { Code, ConnectError, createRouterTransport, type ConnectRouter } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter
} from '@tanstack/react-router'
import { afterEach, describe, expect, it } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { AuthContext } from '#/hooks/use-auth'
import { clearAuth, setAuthGrants, type UserProfile } from '#/libraries/guard/auth-store'
import { RolesView } from '#/routes/(app)/admin/-roles-view'
import { UserDetailView } from '#/routes/(app)/admin/users/-user-detail-view'
import { AuditLogService, ListForUserResponseSchema } from '~/codegen/auditlog_pb'
import { AdminResetUserPasswordResponseSchema, PasswordRecoveryService } from '~/codegen/authn_pb'
import {
  AuthorizationService,
  CreateRoleResponseSchema,
  GetRoleResponseSchema,
  ListPermissionsResponseSchema,
  ListRolesResponseSchema,
  ListUserPermissionsResponseSchema,
  ListUserRolesResponseSchema,
  PermissionSchema,
  RoleDetailSchema,
  RoleSchema,
  RoleType,
  SetRolePermissionsResponseSchema,
  type Role
} from '~/codegen/authz_pb'
import type { ListMetadata } from '~/codegen/common_pb'
import { GetUserResponseSchema, UserSchema, UserService } from '~/codegen/identity_pb'

const EDITORS = create(RoleSchema, {
  id: 'role_editors',
  name: 'Editors',
  slug: 'editors',
  type: RoleType.CUSTOM,
  permissionCount: 2,
  createdAt: '2026-09-01T08:00:00Z'
})

const ADMIN = create(RoleSchema, {
  id: 'role_admin',
  name: 'Administrator',
  slug: 'administrator',
  type: RoleType.SYSTEM,
  permissionCount: 30,
  createdAt: '2026-09-01T08:00:00Z'
})

const CATALOG = [
  create(PermissionSchema, { id: 'perm_1', slug: 'user:*:read', description: 'Read an account' }),
  create(PermissionSchema, { id: 'perm_2', slug: 'user:*:list', description: 'List accounts' }),
  create(PermissionSchema, { id: 'perm_3', slug: 'user_group:*:list', description: 'List groups' })
]

function rolesPage(rows: Role[], metadata?: Partial<ListMetadata>) {
  return create(ListRolesResponseSchema, {
    roles: rows,
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

const notUsed = async () => {
  throw new Error('session surfaces are not part of this page test')
}

function authContext() {
  return {
    user: null,
    loggedIn: true,
    isLoading: false,
    login: notUsed,
    continueSignIn: notUsed,
    completeSignIn: notUsed,
    verifyPasskeyLogin: notUsed,
    requestOneTimeAccess: notUsed,
    exchangeOneTimeToken: notUsed,
    stopImpersonation: async () => {
      throw new Error('the impersonation banner is not part of this page test')
    },
    impersonateUser: async () => {
      throw new Error('impersonation is not part of this page test')
    },
    logout: () => {}
  }
}

function withRouter(children: React.ReactNode, initial = '/') {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: [initial] })
  })
  return <RouterProvider router={router} />
}

function providers(
  children: React.ReactNode,
  transport: ReturnType<typeof createRouterTransport>,
  user: UserProfile | null = null
) {
  return (
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transport}>
        <AuthContext.Provider value={{ ...authContext(), user }}>{children}</AuthContext.Provider>
      </TransportProvider>
    </QueryClientProvider>
  )
}

function baseRoleHandlers(router: ConnectRouter, overrides: Record<string, unknown> = {}) {
  router.service(AuthorizationService, {
    listRoles: () => rolesPage([ADMIN, EDITORS]),
    listPermissions: () =>
      create(ListPermissionsResponseSchema, {
        permissions: CATALOG,
        status: 'success',
        message: 'ok'
      }),
    ...overrides
  })
}

async function clickInDialog(screen: RenderResult, name: string) {
  await expect.element(screen.getByRole('alertdialog')).toBeVisible()
  const button = screen.getByRole('alertdialog').getByRole('button', { name }).element()
  if (!(button instanceof HTMLElement)) throw new Error(`the ${name} button is not an element`)
  button.click()
}

afterEach(() => {
  clearAuth()
})

describe('the roles screen', () => {
  it('renders the roles with their type and set size', async () => {
    const screen = await render(
      withRouter(
        providers(
          <RolesView />,
          createRouterTransport((service) => baseRoleHandlers(service))
        )
      )
    )

    await expect.element(screen.getByText(/Administrator/).first()).toBeVisible()
    await expect.element(screen.getByText(/System/).first()).toBeVisible()
    expect(screen.baseElement.textContent).toContain('editors')
    expect(screen.baseElement.textContent).toContain('2 permissions')
    // A custom role is the administrator's to shape and delete; a system
    // role's row offers only its permission tree.
    await expect.element(screen.getByRole('button', { name: 'Delete' })).toBeVisible()
  })

  it('creates a role with its name and immutable slug', async () => {
    const created: Array<{ name: string; slug: string }> = []
    const screen = await render(
      withRouter(
        providers(
          <RolesView />,
          createRouterTransport((service) =>
            baseRoleHandlers(service, {
              createRole: (request: { name: string; slug: string }) => {
                created.push({ name: request.name, slug: request.slug })
                return create(CreateRoleResponseSchema, {
                  role: create(RoleDetailSchema, {
                    id: 'role_new',
                    name: request.name,
                    slug: request.slug,
                    type: RoleType.CUSTOM,
                    permissions: []
                  }),
                  status: 'success',
                  message: 'ok'
                })
              }
            })
          )
        )
      )
    )

    await screen.getByRole('button', { name: 'Create role' }).click()
    await screen.getByRole('alertdialog').getByLabelText('Name').fill('Reviewers')
    await screen.getByRole('alertdialog').getByLabelText('Slug').fill('reviewers')
    await clickInDialog(screen, 'Create role')

    await expect.poll(() => created.length).toBe(1)
    expect(created[0]).toMatchObject({ name: 'Reviewers', slug: 'reviewers' })
  })

  it('replaces a role’s permission set as a whole and carries the custody copy', async () => {
    const saved: Array<{ id: string; slugs: string[] }> = []
    const screen = await render(
      withRouter(
        providers(
          <RolesView />,
          createRouterTransport((service) =>
            baseRoleHandlers(service, {
              getRole: () =>
                create(GetRoleResponseSchema, {
                  role: create(RoleDetailSchema, {
                    id: 'role_editors',
                    name: 'Editors',
                    slug: 'editors',
                    type: RoleType.CUSTOM,
                    permissions: ['user:*:read']
                  }),
                  status: 'success',
                  message: 'ok'
                }),
              setRolePermissions: (request: { id: string; permissionSlugs: string[] }) => {
                saved.push({ id: request.id, slugs: [...request.permissionSlugs] })
                return create(SetRolePermissionsResponseSchema, {
                  role: create(RoleDetailSchema, {
                    id: request.id,
                    name: 'Editors',
                    slug: 'editors',
                    type: RoleType.CUSTOM,
                    permissions: [...request.permissionSlugs]
                  }),
                  status: 'success',
                  message: 'ok'
                })
              }
            })
          )
        )
      )
    )

    // The custom role's row carries the tree; open it.
    await screen.getByRole('button', { name: 'Permissions' }).last().click()
    await expect.element(screen.getByRole('alertdialog')).toBeVisible()
    expect(screen.baseElement.textContent).toContain('next token')

    // The seed: the role's own set starts checked.
    const readToggle = screen
      .getByRole('alertdialog')
      .getByRole('checkbox', { name: /Permission: user:\*:read/ })
    await expect.element(readToggle).toHaveAttribute('aria-checked', 'true')

    const listBox = screen
      .getByRole('alertdialog')
      .getByRole('checkbox', { name: /Permission: user:\*:list/ })
      .element()
    if (!(listBox instanceof HTMLElement)) throw new Error('the toggle is not an element')
    listBox.click()
    await clickInDialog(screen, 'Save permissions')

    await expect.poll(() => saved.length).toBe(1)
    expect(saved[0]).toMatchObject({ id: 'role_editors', slugs: ['user:*:read', 'user:*:list'] })
  })

  it('opens a system role’s set read-only', async () => {
    const screen = await render(
      withRouter(
        providers(
          <RolesView />,
          createRouterTransport((service) =>
            baseRoleHandlers(service, {
              getRole: () =>
                create(GetRoleResponseSchema, {
                  role: create(RoleDetailSchema, {
                    id: 'role_admin',
                    name: 'Administrator',
                    slug: 'administrator',
                    type: RoleType.SYSTEM,
                    permissions: ['user:*:read', 'user:*:list', 'user_group:*:list']
                  }),
                  status: 'success',
                  message: 'ok'
                })
            })
          )
        )
      )
    )

    // The system role's row offers only its tree — the first Permissions
    // button in the list.
    await screen.getByRole('button', { name: 'Permissions' }).first().click()
    await expect.element(screen.getByRole('alertdialog')).toBeVisible()
    expect(screen.baseElement.textContent).toContain('read-only')
    expect(
      screen.getByRole('alertdialog').getByRole('button', { name: 'Save permissions' }).elements()
        .length
    ).toBe(0)
  })

  it('surfaces the held-by-accounts refusal verbatim', async () => {
    const screen = await render(
      withRouter(
        providers(
          <RolesView />,
          createRouterTransport((service) =>
            baseRoleHandlers(service, {
              deleteRole: () => {
                throw new ConnectError(
                  'the role is still carried by accounts',
                  Code.FailedPrecondition
                )
              }
            })
          )
        )
      )
    )

    await screen.getByRole('button', { name: 'Delete' }).click()
    await clickInDialog(screen, 'Delete role')
    await expect
      .element(screen.getByRole('alertdialog').getByText('the role is still carried by accounts'))
      .toBeVisible()
  })
})

describe('the grants card', () => {
  const LANGDON = create(UserSchema, {
    id: 'usr_langdon',
    username: 'rlangdon',
    email: 'robert@langdon.dev',
    displayName: 'Robert Langdon',
    createdAt: '2026-09-01T08:00:00Z'
  })

  function grantsTransport() {
    return createRouterTransport(({ service }) => {
      service(UserService, {
        getUser: () =>
          create(GetUserResponseSchema, { user: LANGDON, status: 'success', message: 'ok' })
      })
      service(PasswordRecoveryService, {
        adminResetUserPassword: () =>
          create(AdminResetUserPasswordResponseSchema, { status: 'success', message: 'ok' })
      })
      service(AuditLogService, {
        listForUser: () =>
          create(ListForUserResponseSchema, {
            logs: [],
            status: 'success',
            message: 'ok',
            metadata: {
              page: 1,
              limit: 20,
              totalPages: 1,
              totalItems: 0,
              firstItemIndex: 0,
              lastItemIndex: 0
            }
          })
      })
      service(AuthorizationService, {
        listUserRoles: () =>
          create(ListUserRolesResponseSchema, {
            userId: 'usr_langdon',
            roles: [EDITORS],
            status: 'success',
            message: 'ok'
          }),
        listUserPermissions: () =>
          create(ListUserPermissionsResponseSchema, {
            userId: 'usr_langdon',
            permissionSlugs: ['user:*:read'],
            status: 'success',
            message: 'ok'
          }),
        listRoles: () => rolesPage([ADMIN, EDITORS]),
        listPermissions: () =>
          create(ListPermissionsResponseSchema, {
            permissions: CATALOG,
            status: 'success',
            message: 'ok'
          })
      })
    })
  }

  it('edits only behind the assignment grants and says when the grants move', async () => {
    setAuthGrants({
      roles: ['administrator'],
      permissions: ['user:*:read', 'user:*:assign_role', 'user:*:grant_permission']
    })

    const screen = await render(
      withRouter(
        providers(<UserDetailView userId='usr_langdon' />, grantsTransport(), LANGDON),
        '/admin/users/usr_langdon'
      )
    )
    expect(screen.baseElement.textContent).toContain('next token')
    await expect.element(screen.getByRole('button', { name: 'Save roles' })).toBeVisible()
    await expect.element(screen.getByRole('button', { name: 'Save permissions' })).toBeVisible()
  })

  it('reads the card only without the assignment grants', async () => {
    setAuthGrants({ roles: ['administrator'], permissions: ['user:*:read'] })

    const screen = await render(
      withRouter(
        providers(<UserDetailView userId='usr_langdon' />, grantsTransport(), LANGDON),
        '/admin/users/usr_langdon'
      )
    )
    expect(screen.baseElement.textContent).toContain('next token')
    expect(screen.getByRole('button', { name: 'Save roles' }).elements().length).toBe(0)
    expect(screen.getByRole('button', { name: 'Save permissions' }).elements().length).toBe(0)
  })
})
