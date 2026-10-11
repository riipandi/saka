import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { setAuthGrants } from '#/libraries/guard/auth-store'
import { OAuthProvidersView } from '#/routes/(app)/admin/-oauth-providers-view'

// The action triggers sit behind the permission gates, so the file's tests
// run as an administrator holding every action the screen offers.
setAuthGrants({
  roles: ['administrator'],
  permissions: [
    'oauth_connection:*:read',
    'oauth_connection:*:list',
    'oauth_connection:*:create',
    'oauth_connection:*:update',
    'oauth_connection:*:delete'
  ]
})
import {
  CreateOAuthConnectionResponseSchema,
  DeleteOAuthConnectionResponseSchema,
  ListOAuthConnectionsResponseSchema,
  OAuthConnectionSchema,
  OAuthSSOService,
  UpdateOAuthConnectionResponseSchema
} from '~/codegen/authn_pb'
import type { CreateOAuthConnectionRequest, UpdateOAuthConnectionRequest } from '~/codegen/authn_pb'

const GOOGLE = create(OAuthConnectionSchema, {
  id: 'oconn_google',
  kind: 'builtin',
  provider: 'google',
  displayName: 'Google',
  enabled: true,
  scopes: 'openid email profile',
  createdAt: { seconds: 1790000000n, nanos: 0 },
  updatedAt: { seconds: 1790000000n, nanos: 0 }
})

const CUSTOM = create(OAuthConnectionSchema, {
  id: 'oconn_keycloak',
  kind: 'custom',
  provider: 'keycloak',
  displayName: 'Keycloak',
  enabled: false,
  discoveryUrl: 'https://idp.example.com/.well-known/openid-configuration',
  clientId: 'saka-web',
  scopes: 'openid profile email',
  createdAt: { seconds: 1790000000n, nanos: 0 },
  updatedAt: { seconds: 1790000000n, nanos: 0 }
})

let created: CreateOAuthConnectionRequest | undefined
let updated: UpdateOAuthConnectionRequest | undefined

async function mount(connections: Array<typeof GOOGLE>) {
  // The mock keeps its rows: the screen invalidates after every write, so
  // the follow-up listing must answer what the write left behind.
  let rows = [...connections]
  const transport = createRouterTransport(({ service }) => {
    service(OAuthSSOService, {
      listConnections: () => {
        return create(ListOAuthConnectionsResponseSchema, {
          connections: rows,
          status: 'success',
          message: 'the connections were read'
        })
      },
      createConnection: (req) => {
        created = req
        const stored = create(OAuthConnectionSchema, {
          ...GOOGLE,
          provider: req.provider,
          displayName: req.displayName
        })
        rows = [...rows, stored]
        return create(CreateOAuthConnectionResponseSchema, {
          connection: stored,
          status: 'success',
          message: 'the connection was stored'
        })
      },
      updateConnection: (req) => {
        updated = req
        rows = rows.map((row) =>
          row.id === req.id
            ? create(OAuthConnectionSchema, {
                ...row,
                displayName: req.displayName ?? row.displayName
              })
            : row
        )
        return create(UpdateOAuthConnectionResponseSchema, {
          connection: CUSTOM,
          status: 'success',
          message: 'the connection was rewritten'
        })
      },
      deleteConnection: (req) => {
        rows = rows.filter((row) => row.id !== req.id)
        return create(DeleteOAuthConnectionResponseSchema, {
          status: 'success',
          message: 'the connection was removed'
        })
      }
    })
  })

  const view = await render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={new QueryClient()}>
        <OAuthProvidersView />
      </QueryClientProvider>
    </TransportProvider>
  )
  created = undefined
  updated = undefined
  return view
}

/** A click dispatched through the element: the overlay swallows synthetic
 * pointer clicks, and the locator's element may be an SVG the DOM type
 * gives no click() to. */
function press(target: HTMLElement | SVGElement) {
  if (target instanceof HTMLElement) {
    target.click()
    return
  }
  target.dispatchEvent(new MouseEvent('click', { bubbles: true }))
}

/** The dialog helpers: clicks inside an open dialog or confirmation go
 * through the element, not the pointer. The confirmation renders the
 * `alertdialog` role. */
async function confirm(view: RenderResult, name: string) {
  const scope = view.getByRole('dialog').or(view.getByRole('alertdialog'))
  press(scope.getByRole('button', { name }).element())
}

/** A combobox inside the dialog opens by an element click; its options
 * render in the opened content beside it. The index picks among the
 * dialog's comboboxes in DOM order. */
async function pickSelect(view: RenderResult, optionLabel: string, index = 0) {
  press(view.getByRole('dialog').getByRole('combobox').nth(index).element())
  await expect.element(view.getByRole('option', { name: optionLabel })).toBeVisible()
  press(view.getByRole('option', { name: optionLabel }).element())
}

describe('OAuthProvidersView', () => {
  it('lists the registered connections with their kind and state', async () => {
    const view = await mount([GOOGLE, CUSTOM])
    await expect.element(view.getByText('google · Google')).toBeVisible()
    await expect.element(view.getByText('keycloak · Keycloak')).toBeVisible()
    await expect.element(view.getByText(/builtin/)).toBeVisible()
    await expect.element(view.getByText(/custom/)).toBeVisible()
    await expect.element(view.getByText('disabled', { exact: true })).toBeVisible()
  })

  it('adds a builtin provider with the preset defaults', async () => {
    const view = await mount([])
    await view.getByRole('button', { name: 'Add provider' }).click()
    const dialog = view.getByRole('dialog')
    await pickSelect(view, 'Google', 1)
    await dialog.getByLabelText('Client ID').fill('4321.apps.googleusercontent.com')
    await dialog.getByLabelText('Client secret').fill('GOCSPX-secret-value')
    await confirm(view, 'Add provider')
    await expect.element(view.getByText('google · Google')).toBeVisible()
    expect(created?.kind).toBe('builtin')
    expect(created?.provider).toBe('google')
    expect(created?.displayName).toBe('Google')
    expect(created?.enabled).toBe(false)
  })

  it('adds a custom provider from a discovery document and a mapping', async () => {
    const view = await mount([])
    await view.getByRole('button', { name: 'Add provider' }).click()
    const dialog = view.getByRole('dialog')
    await pickSelect(view, 'Custom — any OIDC provider by discovery or manual endpoints', 0)
    await dialog.getByLabelText('Provider slug').fill('keycloak')
    await dialog.getByLabelText('Display name').fill('Keycloak')
    await dialog
      .getByLabelText('Discovery URL')
      .fill('https://idp.example.com/.well-known/openid-configuration')
    await dialog.getByLabelText('Client ID').fill('saka-web')
    await dialog.getByLabelText('Client secret').fill('sealed-value')
    await dialog.getByLabelText('Attribute mapping').fill('{"email": "mail", "subject": "uid"}')
    await confirm(view, 'Add provider')
    await expect.element(view.getByText('keycloak · Keycloak')).toBeVisible()
    expect(created?.kind).toBe('custom')
    expect(created?.discoveryUrl).toBe('https://idp.example.com/.well-known/openid-configuration')
    expect(created?.attributeMapping?.email).toBe('mail')
    expect(created?.attributeMapping?.subject).toBe('uid')
  })

  it('rewrites a connection and keeps the stored secret when left empty', async () => {
    const view = await mount([CUSTOM])
    await expect.element(view.getByText('keycloak · Keycloak')).toBeVisible()
    await view.getByRole('button', { name: 'Edit' }).click()
    const dialog = view.getByRole('dialog')
    await expect.element(dialog.getByLabelText('Display name')).toHaveValue('Keycloak')
    await dialog.getByLabelText('Display name').fill('Company Keycloak')
    await confirm(view, 'Save changes')
    await expect.element(view.getByText('keycloak · Company Keycloak')).toBeVisible()
    expect(updated?.id).toBe('oconn_keycloak')
    expect(updated?.displayName).toBe('Company Keycloak')
    expect(updated?.clientSecret).toBeUndefined()
  })

  it('refuses a mapping that is not valid JSON before the call is made', async () => {
    const view = await mount([])
    await view.getByRole('button', { name: 'Add provider' }).click()
    const dialog = view.getByRole('dialog')
    await pickSelect(view, 'Custom — any OIDC provider by discovery or manual endpoints', 0)
    await dialog.getByLabelText('Provider slug').fill('keycloak')
    await dialog.getByLabelText('Display name').fill('Keycloak')
    await dialog
      .getByLabelText('Discovery URL')
      .fill('https://idp.example.com/.well-known/openid-configuration')
    await dialog.getByLabelText('Client ID').fill('saka-web')
    await dialog.getByLabelText('Client secret').fill('sealed-value')
    await dialog.getByLabelText('Attribute mapping').fill('{email: mail}')
    await confirm(view, 'Add provider')
    await expect.element(view.getByText('the attribute mapping is not valid JSON')).toBeVisible()
    expect(created).toBeUndefined()
  })

  it('removes a connection after the confirmation', async () => {
    const view = await mount([GOOGLE])
    await expect.element(view.getByText('google · Google')).toBeVisible()
    await view.getByRole('button', { name: 'Delete', exact: true }).click()
    await confirm(view, 'Remove')
    await expect.element(view.getByText('No providers yet')).toBeVisible()
  })
})
