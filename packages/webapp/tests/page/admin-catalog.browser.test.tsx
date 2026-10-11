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
import { describe, expect, it } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { setAuthGrants } from '#/libraries/guard/auth-store'
import { BlocklistView } from '#/routes/(app)/admin/-blocklist-view'
import { GroupsView } from '#/routes/(app)/admin/-groups-view'
import { SignupTokensView } from '#/routes/(app)/admin/-signup-tokens-view'

// The action triggers now sit behind the permission gates, so the file's
// tests run as an administrator holding every action the screens offer.
setAuthGrants({
  roles: ['administrator'],
  permissions: [
    'user_group:*:create',
    'user_group:*:update',
    'user_group:*:delete',
    'user_group:*:set_members',
    'blocklist:*:list',
    'blocklist:*:create',
    'blocklist:*:delete',
    'signup_token:*:create',
    'signup_token:*:delete'
  ]
})
import {
  AddBlocklistEntryResponseSchema,
  BlocklistEntrySchema,
  BlocklistService,
  CreateSignupTokenResponseSchema,
  CreateUserGroupResponseSchema,
  DeleteSignupTokenResponseSchema,
  GetUserGroupResponseSchema,
  ListBlocklistEntriesResponseSchema,
  ListSignupTokensResponseSchema,
  ListUserGroupsResponseSchema,
  ListUsersResponseSchema,
  RemoveBlocklistEntryResponseSchema,
  SetUserGroupMembersResponseSchema,
  SignupService,
  SignupTokenSchema,
  UserGroupDetailSchema,
  UserGroupSchema,
  UserGroupService,
  UserSchema,
  UserService
} from '~/codegen/identity_pb'

const LANGDON = create(UserSchema, {
  id: 'usr_langdon',
  username: 'rlangdon',
  email: 'robert@langdon.dev',
  displayName: 'Robert Langdon'
})

const NEVEU = create(UserSchema, {
  id: 'usr_neveu',
  username: 'sneveu',
  email: 'sophie@neveu.dev',
  displayName: 'Sophie Neveu'
})

const EDITORS = create(UserGroupSchema, {
  id: 'ugrp_editors',
  name: 'editors',
  displayName: 'Editors',
  userCount: 1,
  createdAt: '2026-09-01T08:00:00Z'
})

const CURATORS = create(UserGroupSchema, {
  id: 'ugrp_curators',
  name: 'curators',
  displayName: 'Curators',
  userCount: 0,
  createdAt: '2026-09-02T08:00:00Z'
})

const EDITORS_DETAIL = create(UserGroupDetailSchema, {
  id: 'ugrp_editors',
  name: 'editors',
  displayName: 'Editors',
  users: [LANGDON],
  userCount: 1,
  createdAt: '2026-09-01T08:00:00Z'
})

const TOKEN = create(SignupTokenSchema, {
  id: 'stok_1',
  usageLimit: 5,
  usageCount: 2,
  createdAt: '2026-10-01T08:00:00Z',
  expiresAt: '2026-10-08T08:00:00Z'
})

function withRouter(children: React.ReactNode) {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] })
  })
  return <RouterProvider router={router} />
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

describe('the groups screen', () => {
  it('creates a group with its name and display name', async () => {
    const created: Array<{ name: string; displayName: string }> = []
    const transport = createRouterTransport(({ service }) => {
      service(UserGroupService, {
        listUserGroups: () =>
          create(ListUserGroupsResponseSchema, { groups: [EDITORS], status: 'success' }),
        createUserGroup: (request) => {
          created.push({ name: request.name, displayName: request.displayName })
          return create(CreateUserGroupResponseSchema, { group: EDITORS_DETAIL, status: 'success' })
        }
      })
    })

    const view = await mountGroups(transport)

    await view.getByRole('button', { name: 'Create group' }).click()
    await expect.element(view.getByRole('alertdialog')).toBeVisible()
    await view.getByRole('alertdialog').getByLabelText('Name').fill('reviewers')
    await view.getByRole('alertdialog').getByLabelText('Display name').fill('Reviewers')
    await confirmInDialog(view, 'Create group')

    await expect.poll(() => created.length).toBe(1)
    expect(created[0]).toMatchObject({ name: 'reviewers', displayName: 'Reviewers' })
  })

  it('surfaces the duplicate-name refusal verbatim', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(UserGroupService, {
        listUserGroups: () =>
          create(ListUserGroupsResponseSchema, { groups: [EDITORS], status: 'success' }),
        createUserGroup: () => {
          throw new ConnectError('the group name is already taken', Code.AlreadyExists)
        }
      })
    })

    const view = await mountGroups(transport)

    await view.getByRole('button', { name: 'Create group' }).click()
    await view.getByRole('alertdialog').getByLabelText('Name').fill('editors')
    await view.getByRole('alertdialog').getByLabelText('Display name').fill('Editors')
    await confirmInDialog(view, 'Create group')
    await expect
      .element(view.getByRole('alertdialog').getByText('the group name is already taken'))
      .toBeVisible()
  })

  it('replaces the whole member set when the members dialog saves', async () => {
    const members: Array<{ userIds: string[] }> = []
    const transport = createRouterTransport(({ service }) => {
      service(UserGroupService, {
        listUserGroups: () =>
          create(ListUserGroupsResponseSchema, { groups: [EDITORS], status: 'success' }),
        getUserGroup: () =>
          create(GetUserGroupResponseSchema, { group: EDITORS_DETAIL, status: 'success' }),
        setUserGroupMembers: (request) => {
          members.push({ userIds: [...request.userIds] })
          return create(SetUserGroupMembersResponseSchema, {
            group: EDITORS_DETAIL,
            status: 'success'
          })
        }
      })
      service(UserService, {
        listUsers: () =>
          create(ListUsersResponseSchema, { users: [LANGDON, NEVEU], status: 'success' })
      })
    })

    const view = await mountGroups(transport)

    await view.getByRole('button', { name: 'Members' }).click()
    await expect.element(view.getByRole('alertdialog')).toBeVisible()
    // The seed: the group's own member starts checked.
    const langdonToggle = view
      .getByRole('alertdialog')
      .getByRole('checkbox', { name: 'Member: Robert Langdon' })
    await expect.element(langdonToggle).toHaveAttribute('aria-checked', 'true')
    // The addition: the operator checks a second account.
    const neveuBox = view
      .getByRole('alertdialog')
      .getByRole('checkbox', { name: 'Member: Sophie Neveu' })
      .element()
    if (!(neveuBox instanceof HTMLElement)) throw new Error('the toggle is not an element')
    neveuBox.click()
    await confirmInDialog(view, 'Save members')

    await expect.poll(() => members.length).toBe(1)
    expect(members[0]?.userIds.toSorted()).toEqual(['usr_langdon', 'usr_neveu'])
  })
})

function mountGroups(transport: ReturnType<typeof createRouterTransport>): Promise<RenderResult> {
  return render(
    withRouter(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={new QueryClient()}>
          <GroupsView />
        </QueryClientProvider>
      </TransportProvider>
    )
  )
}

describe('the blocklist screen', () => {
  it('adds an entry and removes it by its identifier', async () => {
    const entry = create(BlocklistEntrySchema, {
      id: 'bl_1',
      pattern: '@spammers.example',
      createdBy: 'rlangdon',
      createdAt: { seconds: 1790000000n, nanos: 0 }
    })
    const added: string[] = []
    const removed: string[] = []
    const transport = createRouterTransport(({ service }) => {
      service(BlocklistService, {
        listBlocklistEntries: () =>
          create(ListBlocklistEntriesResponseSchema, { entries: [entry], status: 'success' }),
        addBlocklistEntry: (request) => {
          added.push(request.pattern)
          return create(AddBlocklistEntryResponseSchema, { entry, status: 'success' })
        },
        removeBlocklistEntry: (request) => {
          removed.push(request.id)
          return create(RemoveBlocklistEntryResponseSchema, { status: 'success' })
        }
      })
    })

    const view = await render(
      withRouter(
        <TransportProvider transport={transport}>
          <QueryClientProvider client={new QueryClient()}>
            <BlocklistView />
          </QueryClientProvider>
        </TransportProvider>
      )
    )

    await expect.element(view.getByText('@spammers.example')).toBeVisible()

    await view.getByRole('button', { name: 'Add entry' }).click()
    await view.getByRole('alertdialog').getByLabelText('Identifier').fill('John.Doe@Example.com')
    await confirmInDialog(view, 'Add entry')
    await expect.poll(() => added.length).toBe(1)
    // The value travels as typed; the storage lowercases it.
    expect(added[0]).toBe('John.Doe@Example.com')

    await view.getByRole('button', { name: 'Remove' }).click()
    await confirmInDialog(view, 'Remove')
    await expect.poll(() => removed.length).toBe(1)
    expect(removed[0]).toBe('bl_1')
  })
})

describe('the signup tokens screen', () => {
  it('issues a token and shows the raw value once', async () => {
    const issued: Array<{ ttlSeconds: number; usageLimit?: number; groups: string[] }> = []
    const transport = createRouterTransport(({ service }) => {
      service(SignupService, {
        listSignupTokens: () =>
          create(ListSignupTokensResponseSchema, { tokens: [TOKEN], status: 'success' }),
        createSignupToken: (request) => {
          issued.push({
            ttlSeconds: request.ttlSeconds,
            usageLimit: request.usageLimit,
            groups: [...request.userGroupIds]
          })
          return create(CreateSignupTokenResponseSchema, {
            token: TOKEN,
            rawToken: 'stv_raw_once_only',
            status: 'success'
          })
        }
      })
      service(UserGroupService, {
        listUserGroups: () =>
          create(ListUserGroupsResponseSchema, { groups: [EDITORS, CURATORS], status: 'success' })
      })
    })

    const view = await render(
      withRouter(
        <TransportProvider transport={transport}>
          <QueryClientProvider client={new QueryClient()}>
            <SignupTokensView />
          </QueryClientProvider>
        </TransportProvider>
      )
    )

    await expect.element(view.getByText(/2\/5/)).toBeVisible()

    await view.getByRole('button', { name: 'Issue token' }).click()
    await view.getByRole('alertdialog').getByLabelText('Usage limit').fill('10')
    const editorsBox = view
      .getByRole('alertdialog')
      .getByRole('checkbox', { name: 'Group: Editors' })
      .element()
    if (!(editorsBox instanceof HTMLElement)) throw new Error('the toggle is not an element')
    editorsBox.click()
    await confirmInDialog(view, 'Issue token')

    await expect.poll(() => issued.length).toBe(1)
    expect(issued[0]).toMatchObject({ ttlSeconds: 86400, usageLimit: 10, groups: ['ugrp_editors'] })

    // The show-once surface: the raw value, and the dialog names it.
    await expect.element(view.getByText('stv_raw_once_only')).toBeVisible()
    expect(view.baseElement.textContent).toContain('shown once')
  })

  it('revokes an unspent invitation by its identifier', async () => {
    const revoked: string[] = []
    const transport = createRouterTransport(({ service }) => {
      service(SignupService, {
        listSignupTokens: () =>
          create(ListSignupTokensResponseSchema, { tokens: [TOKEN], status: 'success' }),
        deleteSignupToken: (request) => {
          revoked.push(request.id)
          return create(DeleteSignupTokenResponseSchema, { status: 'success' })
        }
      })
    })

    const view = await render(
      withRouter(
        <TransportProvider transport={transport}>
          <QueryClientProvider client={new QueryClient()}>
            <SignupTokensView />
          </QueryClientProvider>
        </TransportProvider>
      )
    )

    await view.getByRole('button', { name: 'Revoke' }).click()
    await confirmInDialog(view, 'Revoke')
    await expect.poll(() => revoked.length).toBe(1)
    expect(revoked[0]).toBe('stok_1')
  })
})
