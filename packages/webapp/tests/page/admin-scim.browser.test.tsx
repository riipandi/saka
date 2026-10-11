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
import { OidcClientDetailView } from '#/routes/(app)/admin/oidc-clients/-oidc-client-detail-view'

// The action triggers sit behind the permission gates, so the file's tests
// run as an administrator holding every action the card offers.
setAuthGrants({
  roles: ['administrator'],
  permissions: [
    'oidc_client:*:read',
    'oidc_client:*:list',
    'scim_provider:*:read',
    'scim_provider:*:create',
    'scim_provider:*:update',
    'scim_provider:*:delete',
    'scim_provider:*:sync'
  ]
})
import {
  CreateScimProviderResponseSchema,
  GetOidcClientResponseSchema,
  GetScimProviderResponseSchema,
  OidcClientCredentialsSchema,
  OidcClientSchema,
  OidcClientService,
  SyncScimProviderResponseSchema,
  UpdateScimProviderResponseSchema,
  DeleteScimProviderResponseSchema
} from '~/codegen/federation_pb'
import { ScimProviderSchema, ScimProviderService } from '~/codegen/federation_pb'
import {
  ListUserGroupsResponseSchema,
  UserGroupSchema,
  UserGroupService
} from '~/codegen/identity_pb'

const GALLERY = create(OidcClientSchema, {
  id: 'gallery',
  name: 'Gallery',
  callbackUrls: ['https://gallery.example.com/callback'],
  clientType: 'standard',
  isGroupRestricted: false,
  hasLogo: false,
  createdAt: { seconds: 1790000000n, nanos: 0 },
  credentials: create(OidcClientCredentialsSchema, { secrets: [] }),
  allowedUserGroups: []
})

const PROVIDER = create(ScimProviderSchema, {
  id: 'prov_1',
  clientId: 'gallery',
  endpoint: 'https://target.example.com/scim/v2',
  createdAt: { seconds: 1790000000n, nanos: 0 }
})

const EDITORS = create(UserGroupSchema, { id: 'ugrp_editors', name: 'editors' })

function withRouter(children: React.ReactNode) {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] })
  })
  return <RouterProvider router={router} />
}

function mount(
  transport: ReturnType<typeof createRouterTransport>,
  clientId = 'gallery'
): Promise<RenderResult> {
  return render(
    withRouter(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={new QueryClient()}>
          <OidcClientDetailView clientId={clientId} />
        </QueryClientProvider>
      </TransportProvider>
    )
  )
}

/** The dialog overlay intercepts pointer events, so a confirm click inside
 * an open dialog goes through the element itself. */
function confirm(view: RenderResult, name: string) {
  const button = view.getByRole('dialog').getByRole('button', { name }).element()
  if (!(button instanceof HTMLElement)) throw new Error(`the ${name} button is not an element`)
  button.click()
}

describe('the scim provider card', () => {
  it('attaches a target and shows the token once', async () => {
    const created: Array<{ clientId: string; endpoint: string; token: string }> = []
    const transport = createRouterTransport(({ service }) => {
      service(OidcClientService, {
        getClient: () => create(GetOidcClientResponseSchema, { client: GALLERY, status: 'success' })
      })
      service(ScimProviderService, {
        getByClient: () => {
          throw new ConnectError('scimsync: the client has no provider', Code.NotFound)
        },
        create: (request) => {
          created.push({
            clientId: request.clientId,
            endpoint: request.endpoint,
            token: request.token
          })
          return create(CreateScimProviderResponseSchema, {
            provider: create(ScimProviderSchema, { ...PROVIDER, token: 'scim_raw_once_only' }),
            status: 'success'
          })
        }
      })
      service(UserGroupService, {
        listUserGroups: () =>
          create(ListUserGroupsResponseSchema, { groups: [EDITORS], status: 'success' })
      })
    })

    const view = await mount(transport)

    await expect
      .element(view.getByText('No provisioning target is attached to this client.'))
      .toBeVisible()
    await view.getByRole('button', { name: 'Attach provider' }).click()
    await expect.element(view.getByRole('dialog')).toBeVisible()
    await view
      .getByRole('dialog')
      .getByLabelText('Endpoint')
      .fill('https://target.example.com/scim/v2')
    await view.getByRole('dialog').getByLabelText('Bearer token').fill('scim_raw_once_only')
    confirm(view, 'Attach')

    await expect.poll(() => created.length).toBe(1)
    expect(created[0]).toMatchObject({
      clientId: 'gallery',
      endpoint: 'https://target.example.com/scim/v2',
      token: 'scim_raw_once_only'
    })

    // The token's one read, then it is sealed.
    await expect.element(view.getByLabelText('Secret')).toHaveValue('scim_raw_once_only')
    // The dialog overlay intercepts pointer events; the confirm pattern goes
    // through the element itself.
    const acknowledge = view.getByRole('button', { name: 'I have stored it' }).element()
    if (!(acknowledge instanceof HTMLElement)) {
      throw new Error('the acknowledge button is not an element')
    }
    acknowledge.click()
    expect(view.getByText('scim_raw_once_only').elements().length).toBe(0)
  })

  it('shows the target and runs a sync pass on demand', async () => {
    const synced: string[] = []
    const transport = createRouterTransport(({ service }) => {
      service(OidcClientService, {
        getClient: () => create(GetOidcClientResponseSchema, { client: GALLERY, status: 'success' })
      })
      service(ScimProviderService, {
        getByClient: () =>
          create(GetScimProviderResponseSchema, { provider: PROVIDER, status: 'success' }),
        sync: (request) => {
          synced.push(request.id)
          return create(SyncScimProviderResponseSchema, {
            usersCreated: 2,
            usersUpdated: 1,
            usersDeleted: 0,
            groupsCreated: 1,
            groupsUpdated: 0,
            groupsDeleted: 0,
            status: 'success'
          })
        }
      })
      service(UserGroupService, {
        listUserGroups: () =>
          create(ListUserGroupsResponseSchema, { groups: [EDITORS], status: 'success' })
      })
    })

    const view = await mount(transport)

    await expect.element(view.getByText(/target\.example\.com\/scim\/v2/)).toBeVisible()
    await view.getByRole('button', { name: 'Sync now' }).click()

    await expect.poll(() => synced.length).toBe(1)
    expect(synced[0]).toBe('prov_1')
    await expect.element(view.getByText(/2 accounts created, 1 updated/)).toBeVisible()
  })

  it('replaces the endpoint and keeps the token when left empty', async () => {
    const updates: Array<{ id: string; endpoint: string; token: string }> = []
    const transport = createRouterTransport(({ service }) => {
      service(OidcClientService, {
        getClient: () => create(GetOidcClientResponseSchema, { client: GALLERY, status: 'success' })
      })
      service(ScimProviderService, {
        getByClient: () =>
          create(GetScimProviderResponseSchema, { provider: PROVIDER, status: 'success' }),
        update: (request) => {
          updates.push({ id: request.id, endpoint: request.endpoint, token: request.token })
          return create(UpdateScimProviderResponseSchema, { provider: PROVIDER, status: 'success' })
        }
      })
      service(UserGroupService, {
        listUserGroups: () =>
          create(ListUserGroupsResponseSchema, { groups: [EDITORS], status: 'success' })
      })
    })

    const view = await mount(transport)

    await view.getByRole('button', { name: 'Update' }).click()
    await expect.element(view.getByRole('dialog')).toBeVisible()
    await expect
      .element(view.getByRole('dialog').getByLabelText('Endpoint'))
      .toHaveValue('https://target.example.com/scim/v2')
    await view
      .getByRole('dialog')
      .getByLabelText('Endpoint')
      .fill('https://other.example.com/scim/v2')
    confirm(view, 'Save target')

    await expect.poll(() => updates.length).toBe(1)
    // The empty token keeps the stored one — the wire's own contract.
    expect(updates[0]).toMatchObject({
      id: 'prov_1',
      endpoint: 'https://other.example.com/scim/v2',
      token: ''
    })
  })

  it('detaches the target behind the confirmation', async () => {
    const detached: string[] = []
    const transport = createRouterTransport(({ service }) => {
      service(OidcClientService, {
        getClient: () => create(GetOidcClientResponseSchema, { client: GALLERY, status: 'success' })
      })
      service(ScimProviderService, {
        getByClient: () =>
          create(GetScimProviderResponseSchema, { provider: PROVIDER, status: 'success' }),
        delete: (request) => {
          detached.push(request.id)
          return create(DeleteScimProviderResponseSchema, { status: 'success' })
        }
      })
      service(UserGroupService, {
        listUserGroups: () =>
          create(ListUserGroupsResponseSchema, { groups: [EDITORS], status: 'success' })
      })
    })

    const view = await mount(transport)

    await view.getByRole('button', { name: 'Detach' }).click()
    await expect.element(view.getByRole('alertdialog')).toBeVisible()
    const confirmButton = view
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Detach' })
      .element()
    if (!(confirmButton instanceof HTMLElement)) throw new Error('the confirm is not an element')
    confirmButton.click()

    await expect.poll(() => detached.length).toBe(1)
    expect(detached[0]).toBe('prov_1')
  })
})
