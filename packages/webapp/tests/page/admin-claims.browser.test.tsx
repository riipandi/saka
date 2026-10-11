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
import { ClaimsView } from '#/routes/(app)/admin/-claims-view'

// The action triggers sit behind the permission gates, so the file's tests
// run as an administrator holding every action the screen offers.
setAuthGrants({
  roles: ['administrator'],
  permissions: [
    'custom_claim:*:list',
    'custom_claim:*:read',
    'custom_claim:*:create',
    'custom_claim:*:update',
    'custom_claim:*:delete'
  ]
})
import {
  CreateUserCustomClaimResponseSchema,
  DeleteUserCustomClaimResponseSchema,
  ListGroupCustomClaimsResponseSchema,
  ListUserCustomClaimsResponseSchema,
  CustomClaimSchema,
  CustomClaimService,
  SuggestCustomClaimsResponseSchema,
  SuggestedCustomClaimKeySchema,
  UpdateUserCustomClaimResponseSchema
} from '~/codegen/federation_pb'
import {
  ListUserGroupsResponseSchema,
  ListUsersResponseSchema,
  UserGroupSchema,
  UserSchema,
  UserGroupService,
  UserService
} from '~/codegen/identity_pb'

const LANGDON = create(UserSchema, {
  id: 'usr_langdon',
  username: 'rlangdon',
  email: 'robert@langdon.dev',
  displayName: 'Robert Langdon'
})

const EDITORS = create(UserGroupSchema, { id: 'ugrp_editors', name: 'editors' })

const DEPARTMENT = create(CustomClaimSchema, {
  id: 'cclm_1',
  key: 'department',
  value: 'crypto'
})

const CLEARANCE = create(CustomClaimSchema, {
  id: 'cclm_2',
  key: 'clearance',
  value: '{"level": 3}'
})

function withRouter(children: React.ReactNode) {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] })
  })
  return <RouterProvider router={router} />
}

function mount(transport: ReturnType<typeof createRouterTransport>) {
  return render(
    withRouter(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={new QueryClient()}>
          <ClaimsView />
        </QueryClientProvider>
      </TransportProvider>
    )
  )
}

/** The uilibs Select opens its options on a trigger click; the option rows
 * are `option` roles inside the opened content. */
async function pickOption(view: RenderResult, label: string) {
  await view.getByRole('combobox').click()
  await view.getByRole('option', { name: label }).click()
}

describe('the custom claims screen', () => {
  it("adds a claim to the chosen account with the wire's key and value", async () => {
    const added: Array<{ userId: string; key: string; value: string }> = []
    const transport = createRouterTransport(({ service }) => {
      service(UserService, {
        listUsers: () => create(ListUsersResponseSchema, { users: [LANGDON], status: 'success' })
      })
      service(CustomClaimService, {
        listUserClaims: () =>
          create(ListUserCustomClaimsResponseSchema, { claims: [], status: 'success' }),
        createUserClaim: (request) => {
          added.push({ userId: request.userId, key: request.key, value: request.value })
          return create(CreateUserCustomClaimResponseSchema, {
            claim: DEPARTMENT,
            status: 'success'
          })
        }
      })
    })

    const view = await mount(transport)

    // The subject picker comes first — the claims card answers the pick.
    await pickOption(view, 'Robert Langdon')
    await view.getByRole('button', { name: 'Add claim' }).click()
    await expect.element(view.getByRole('dialog')).toBeVisible()
    await view.getByRole('dialog').getByLabelText('Key').fill('department')
    await view.getByRole('dialog').getByLabelText('Value').fill('crypto')
    const submit = view.getByRole('dialog').getByRole('button', { name: 'Add claim' }).element()
    if (!(submit instanceof HTMLElement)) throw new Error('the submit is not an element')
    submit.click()

    await expect.poll(() => added.length).toBe(1)
    expect(added[0]).toMatchObject({ userId: 'usr_langdon', key: 'department', value: 'crypto' })
  })

  it("offers the deployment's own keys as suggestions", async () => {
    const transport = createRouterTransport(({ service }) => {
      service(UserService, {
        listUsers: () => create(ListUsersResponseSchema, { users: [LANGDON], status: 'success' })
      })
      service(CustomClaimService, {
        listUserClaims: () =>
          create(ListUserCustomClaimsResponseSchema, { claims: [], status: 'success' }),
        suggest: () =>
          create(SuggestCustomClaimsResponseSchema, {
            keys: [create(SuggestedCustomClaimKeySchema, { key: 'department', usageCount: 12n })],
            status: 'success'
          })
      })
    })

    const view = await mount(transport)

    await pickOption(view, 'Robert Langdon')
    await view.getByRole('button', { name: 'Add claim' }).click()
    await expect.element(view.getByRole('dialog')).toBeVisible()
    const chip = view.getByRole('dialog').getByRole('button', { name: 'department (12)' }).element()
    if (!(chip instanceof HTMLElement)) throw new Error('the suggestion chip is not an element')
    chip.click()
    await expect.element(view.getByRole('dialog').getByLabelText('Key')).toHaveValue('department')
  })

  it('rewrites a claim through the edit dialog', async () => {
    const updates: Array<{ id: string; key: string; value: string }> = []
    const transport = createRouterTransport(({ service }) => {
      service(UserService, {
        listUsers: () => create(ListUsersResponseSchema, { users: [LANGDON], status: 'success' })
      })
      service(CustomClaimService, {
        listUserClaims: () =>
          create(ListUserCustomClaimsResponseSchema, {
            claims: [DEPARTMENT, CLEARANCE],
            status: 'success'
          }),
        updateUserClaim: (request) => {
          updates.push({ id: request.id, key: request.key, value: request.value })
          return create(UpdateUserCustomClaimResponseSchema, {
            claim: DEPARTMENT,
            status: 'success'
          })
        }
      })
    })

    const view = await mount(transport)

    await pickOption(view, 'Robert Langdon')
    await expect.element(view.getByText('department')).toBeVisible()
    await view.getByRole('button', { name: 'Edit' }).first().click()
    await expect.element(view.getByRole('dialog')).toBeVisible()
    await expect.element(view.getByRole('dialog').getByLabelText('Key')).toHaveValue('department')
    await view.getByRole('dialog').getByLabelText('Value').fill('applied cryptography')
    const submit = view.getByRole('dialog').getByRole('button', { name: 'Save claim' }).element()
    if (!(submit instanceof HTMLElement)) throw new Error('the submit is not an element')
    submit.click()

    await expect.poll(() => updates.length).toBe(1)
    expect(updates[0]).toMatchObject({
      id: 'cclm_1',
      key: 'department',
      value: 'applied cryptography'
    })
  })

  it('deletes a claim behind the confirmation', async () => {
    const deleted: string[] = []
    const transport = createRouterTransport(({ service }) => {
      service(UserService, {
        listUsers: () => create(ListUsersResponseSchema, { users: [LANGDON], status: 'success' })
      })
      service(CustomClaimService, {
        listUserClaims: () =>
          create(ListUserCustomClaimsResponseSchema, { claims: [DEPARTMENT], status: 'success' }),
        deleteUserClaim: (request) => {
          deleted.push(request.id)
          return create(DeleteUserCustomClaimResponseSchema, { status: 'success' })
        }
      })
    })

    const view = await mount(transport)

    await pickOption(view, 'Robert Langdon')
    await expect.element(view.getByText('department')).toBeVisible()
    await view.getByRole('button', { name: 'Delete' }).click()
    await expect.element(view.getByRole('alertdialog')).toBeVisible()
    const confirm = view
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Delete claim' })
      .element()
    if (!(confirm instanceof HTMLElement)) throw new Error('the confirm is not an element')
    confirm.click()

    await expect.poll(() => deleted.length).toBe(1)
    expect(deleted[0]).toBe('cclm_1')
  })

  it('surfaces the reserved-key refusal verbatim', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(UserService, {
        listUsers: () => create(ListUsersResponseSchema, { users: [LANGDON], status: 'success' })
      })
      service(CustomClaimService, {
        listUserClaims: () =>
          create(ListUserCustomClaimsResponseSchema, { claims: [], status: 'success' }),
        createUserClaim: () => {
          throw new ConnectError(
            'customclaim: the key is reserved by the protocol',
            Code.FailedPrecondition
          )
        }
      })
    })

    const view = await mount(transport)

    await pickOption(view, 'Robert Langdon')
    await view.getByRole('button', { name: 'Add claim' }).click()
    await view.getByRole('dialog').getByLabelText('Key').fill('sub')
    await view.getByRole('dialog').getByLabelText('Value').fill('usr_langdon')
    const submit = view.getByRole('dialog').getByRole('button', { name: 'Add claim' }).element()
    if (!(submit instanceof HTMLElement)) throw new Error('the submit is not an element')
    submit.click()
    await expect
      .element(
        view.getByRole('dialog').getByText('customclaim: the key is reserved by the protocol')
      )
      .toBeVisible()
  })

  it("lists a group's claims on the group tab", async () => {
    const transport = createRouterTransport(({ service }) => {
      service(UserGroupService, {
        listUserGroups: () =>
          create(ListUserGroupsResponseSchema, { groups: [EDITORS], status: 'success' })
      })
      service(CustomClaimService, {
        listGroupClaims: () =>
          create(ListGroupCustomClaimsResponseSchema, { claims: [CLEARANCE], status: 'success' })
      })
    })

    const view = await mount(transport)

    await view.getByRole('tab', { name: 'Group claims' }).click()
    await pickOption(view, 'editors')
    await expect.element(view.getByText('clearance')).toBeVisible()
    await expect.element(view.getByText('{"level": 3}')).toBeVisible()
  })
})
