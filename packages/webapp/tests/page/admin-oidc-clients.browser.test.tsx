import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
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
import { OidcClientNewView } from '#/routes/(app)/admin/oidc-clients/-oidc-client-new-view'
import { OidcClientsView } from '#/routes/(app)/admin/oidc-clients/-oidc-clients-view'

// The action triggers sit behind the permission gates, so the file's tests
// run as an administrator holding every action the screens offer.
setAuthGrants({
  roles: ['administrator'],
  permissions: [
    'oidc_client:*:list',
    'oidc_client:*:create',
    'oidc_client:*:update',
    'oidc_client:*:delete'
  ]
})
import { ListMetadataSchema } from '~/codegen/common_pb'
import {
  CreateOidcClientResponseSchema,
  CreateOidcClientSecretResponseSchema,
  DeleteOidcClientSecretResponseSchema,
  GetOidcClientResponseSchema,
  ListOidcClientSecretsResponseSchema,
  ListOidcClientsResponseSchema,
  OidcClientCredentialsSchema,
  OidcClientSchema,
  OidcClientSecretSchema,
  OidcClientService,
  RefreshOidcClientResponseSchema,
  UpdateOidcClientAllowedUserGroupsResponseSchema,
  type OidcClient,
  type OidcClientSecret
} from '~/codegen/federation_pb'
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
  credentials: create(OidcClientCredentialsSchema, {
    secrets: [
      create(OidcClientSecretSchema, {
        id: 'ocs_1',
        prefix: 'abcd',
        isActive: true,
        createdAt: { seconds: 1790000000n, nanos: 0 }
      })
    ]
  }),
  allowedUserGroups: []
})

const PORTAL = create(OidcClientSchema, {
  id: 'portal-cimd',
  name: 'Portal',
  callbackUrls: ['https://portal.example.com/callback'],
  clientType: 'cimd',
  isPublic: true,
  isGroupRestricted: true,
  hasLogo: false,
  createdAt: { seconds: 1790000000n, nanos: 0 },
  credentials: create(OidcClientCredentialsSchema, { secrets: [] }),
  allowedUserGroups: []
})

const EDITORS = create(UserGroupSchema, { id: 'ugrp_editors', name: 'editors' })
const CURATORS = create(UserGroupSchema, { id: 'ugrp_curators', name: 'curators' })

function withRouter(children: React.ReactNode) {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] })
  })
  return <RouterProvider router={router} />
}

function pageMeta(rows: number) {
  return create(ListMetadataSchema, {
    page: 1,
    limit: 20,
    totalItems: rows,
    firstItemIndex: 0,
    lastItemIndex: rows
  })
}

function mount(view: React.ReactNode, transport: ReturnType<typeof createRouterTransport>) {
  return render(
    withRouter(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={new QueryClient()}>{view}</QueryClientProvider>
      </TransportProvider>
    )
  )
}

/** The dialog overlay intercepts pointer events, so a confirm click inside
 * an open dialog goes through the element itself. */
async function confirm(view: RenderResult, name: string) {
  await expect.element(view.getByRole('alertdialog')).toBeVisible()
  const button = view.getByRole('alertdialog').getByRole('button', { name }).element()
  if (!(button instanceof HTMLElement)) throw new Error(`the ${name} button is not an element`)
  button.click()
}

describe('the oidc clients list', () => {
  it('shows the clients and their markers', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(OidcClientService, {
        listClients: () =>
          create(ListOidcClientsResponseSchema, {
            clients: [GALLERY, PORTAL],
            metadata: pageMeta(2),
            status: 'success'
          })
      })
    })

    const view = await mount(<OidcClientsView />, transport)

    await expect.element(view.getByText('Gallery')).toBeVisible()
    await expect.element(view.getByText('Portal')).toBeVisible()
    // The markers the row carries: the CIMD origin, the public client, and
    // the group restriction.
    expect(view.getByText('CIMD').elements().length).toBe(1)
    expect(view.getByText('Public').elements().length).toBe(1)
    expect(view.getByText('Restricted').elements().length).toBe(1)
  })
})

describe('the create screen', () => {
  it('parses the callback lines and carries the grant choices to the wire', async () => {
    const created: Array<{
      callbackUrls: string[]
      allowedGrantTypes: string[]
      backchannelLogoutSessionRequired: boolean
    }> = []
    const transport = createRouterTransport(({ service }) => {
      service(OidcClientService, {
        createClient: (request) => {
          created.push({
            callbackUrls: [...request.callbackUrls],
            allowedGrantTypes: [...request.allowedGrantTypes],
            backchannelLogoutSessionRequired: request.backchannelLogoutSessionRequired
          })
          return create(CreateOidcClientResponseSchema, {
            client: GALLERY,
            secret: 'oc_raw_once_only',
            status: 'success'
          })
        }
      })
    })

    const view = await mount(<OidcClientNewView />, transport)

    await view.getByLabelText('Name').fill('Gallery')
    await view
      .getByLabelText('Callback URLs')
      .fill('https://gallery.example.com/callback\nhttps://gallery.example.com/oidc/callback')
    const deviceGrant = view
      .getByRole('checkbox', { name: /urn:ietf:params:oauth:grant-type:device_code/ })
      .element()
    if (!(deviceGrant instanceof HTMLElement)) throw new Error('the toggle is not an element')
    deviceGrant.click()
    const backchannel = view
      .getByRole('checkbox', { name: 'Require the session identity in the logout token' })
      .element()
    if (!(backchannel instanceof HTMLElement)) throw new Error('the toggle is not an element')
    backchannel.click()
    await view.getByRole('button', { name: 'Create client' }).click()

    await expect.poll(() => created.length).toBe(1)
    expect(created[0]?.callbackUrls).toEqual([
      'https://gallery.example.com/callback',
      'https://gallery.example.com/oidc/callback'
    ])
    expect(created[0]?.allowedGrantTypes).toEqual(['urn:ietf:params:oauth:grant-type:device_code'])
    expect(created[0]?.backchannelLogoutSessionRequired).toBe(true)

    // The minting's answer: the raw secret, shown once.
    await expect.element(view.getByLabelText('Secret')).toHaveValue('oc_raw_once_only')
    expect(view.baseElement.textContent).toContain('shown only once')
  })

  it('refuses a client with no callback URL at the form', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(OidcClientService, {
        createClient: () => {
          throw new Error('the create must never fire')
        }
      })
    })

    const view = await mount(<OidcClientNewView />, transport)

    await view.getByLabelText('Name').fill('Headless')
    await view.getByRole('button', { name: 'Create client' }).click()
    await expect
      .element(view.getByText('A client with no callback URL cannot receive a response.'))
      .toBeVisible()
  })
})

/** The detail's transport: the client, its secrets, and the handlers the
 * individual tests record their captures through. */
function detailTransport(handlers: {
  getClient?: () => OidcClient
  listSecrets?: () => OidcClientSecret[]
  mint?: (value: string) => void
  withdraw?: (secretId: string) => void
  saveGroups?: (ids: string[]) => void
  refresh?: (id: string) => void
}) {
  const client = handlers.getClient ? handlers.getClient() : GALLERY
  const secrets = handlers.listSecrets
    ? handlers.listSecrets()
    : (GALLERY.credentials?.secrets ?? [])
  return createRouterTransport(({ service }) => {
    service(OidcClientService, {
      getClient: () => create(GetOidcClientResponseSchema, { client, status: 'success' }),
      listSecrets: () =>
        create(ListOidcClientSecretsResponseSchema, {
          credentials: create(OidcClientCredentialsSchema, { secrets }),
          status: 'success'
        }),
      createSecret: (request) => {
        handlers.mint?.(request.secret ?? '')
        return create(CreateOidcClientSecretResponseSchema, {
          secret: create(OidcClientSecretSchema, {
            id: 'ocs_2',
            prefix: 'wxyz',
            isActive: true,
            createdAt: { seconds: 1790000001n, nanos: 0 }
          }),
          value: 'oc_raw_second',
          status: 'success'
        })
      },
      deleteSecret: (request) => {
        handlers.withdraw?.(request.secretId)
        return create(DeleteOidcClientSecretResponseSchema, { status: 'success' })
      },
      updateAllowedUserGroups: (request) => {
        handlers.saveGroups?.([...request.userGroupIds])
        return create(UpdateOidcClientAllowedUserGroupsResponseSchema, {
          client,
          status: 'success'
        })
      },
      refreshClient: (request) => {
        handlers.refresh?.(request.id)
        return create(RefreshOidcClientResponseSchema, { client, status: 'success' })
      }
    })
    service(UserGroupService, {
      listUserGroups: () =>
        create(ListUserGroupsResponseSchema, { groups: [EDITORS, CURATORS], status: 'success' })
    })
  })
}

describe('the client detail', () => {
  it('lists the secret prefixes, mints another, and withdraws one', async () => {
    const minted: string[] = []
    const withdrawn: string[] = []
    const view = await mount(
      <OidcClientDetailView clientId='gallery' />,
      detailTransport({
        mint: (custom) => minted.push(custom),
        withdraw: (secretId) => withdrawn.push(secretId)
      })
    )

    await expect.element(view.getByText(/abcd••••••••/)).toBeVisible()

    await view.getByRole('button', { name: 'Mint secret' }).click()
    // The dialog overlay intercepts pointer events; the confirm pattern goes
    // through the element itself.
    const mintButton = view.getByRole('dialog').getByRole('button', { name: 'Mint' }).element()
    if (!(mintButton instanceof HTMLElement)) throw new Error('the mint button is not an element')
    mintButton.click()
    await expect.element(view.getByLabelText('Secret')).toHaveValue('oc_raw_second')

    // The acknowledgement closes the show-once surface and the value is gone.
    const acknowledge = view.getByRole('button', { name: 'I have stored it' }).element()
    if (!(acknowledge instanceof HTMLElement)) {
      throw new Error('the acknowledge button is not an element')
    }
    acknowledge.click()
    expect(view.getByText('oc_raw_second').elements().length).toBe(0)

    await view.getByRole('button', { name: 'Withdraw' }).click()
    await confirm(view, 'Withdraw')
    await expect.poll(() => withdrawn.length).toBe(1)
    expect(withdrawn[0]).toBe('ocs_1')
  })

  it('replaces the restriction set from the groups card', async () => {
    const restricted = create(OidcClientSchema, {
      ...GALLERY,
      isGroupRestricted: true,
      allowedUserGroups: [EDITORS]
    })
    const saved: string[][] = []
    const view = await mount(
      <OidcClientDetailView clientId='gallery' />,
      detailTransport({
        getClient: () => restricted,
        saveGroups: (ids) => saved.push(ids)
      })
    )

    // The seed: the restriction's own group starts checked.
    await expect
      .element(view.getByRole('checkbox', { name: 'editors' }))
      .toHaveAttribute('aria-checked', 'true')
    const curators = view.getByRole('checkbox', { name: 'curators' }).element()
    if (!(curators instanceof HTMLElement)) throw new Error('the toggle is not an element')
    curators.click()
    await view.getByRole('button', { name: 'Save groups' }).click()

    await expect.poll(() => saved.length).toBe(1)
    expect(saved[0]?.toSorted()).toEqual(['ugrp_curators', 'ugrp_editors'])
  })

  it('offers the metadata refresh to a CIMD client alone', async () => {
    const refreshed: string[] = []
    const standard = await mount(<OidcClientDetailView clientId='gallery' />, detailTransport({}))
    expect(standard.getByRole('button', { name: 'Refresh metadata' }).elements().length).toBe(0)

    const cimd = create(OidcClientSchema, { ...PORTAL, clientType: 'cimd' })
    const portal = await mount(
      <OidcClientDetailView clientId='portal-cimd' />,
      detailTransport({ getClient: () => cimd, refresh: (id) => refreshed.push(id) })
    )
    await portal.getByRole('button', { name: 'Refresh metadata' }).click()
    await expect.poll(() => refreshed.length).toBe(1)
    expect(refreshed[0]).toBe('portal-cimd')
  })
})
