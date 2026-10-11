import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { AuthorizedAppsView } from '#/routes/(app)/account/-authorized-apps-view'
import type { AuthorizedOidcClient } from '~/codegen/federation_pb'
import {
  AuthorizedOidcClientSchema,
  ListMyAuthorizedClientsResponseSchema,
  OidcClientSchema,
  OidcConsentService,
  RevokeMyAuthorizedClientResponseSchema
} from '~/codegen/federation_pb'

function consent(clientId: string, name: string, scopes: string[]): AuthorizedOidcClient {
  return create(AuthorizedOidcClientSchema, {
    client: create(OidcClientSchema, { id: clientId, name, callbackUrls: [] }),
    scopes,
    lastUsedAt: { seconds: 1790000000n, nanos: 0 }
  })
}

let revoked: string | undefined

async function renderApps(clients: Array<AuthorizedOidcClient>) {
  let rows = [...clients]
  const transport = createRouterTransport(({ service }) => {
    service(OidcConsentService, {
      listMyAuthorizedClients: () =>
        create(ListMyAuthorizedClientsResponseSchema, {
          clients: rows,
          status: 'success',
          message: 'the ledger was read'
        }),
      revokeMyAuthorizedClient: (request) => {
        revoked = request.clientId
        rows = rows.filter((row) => row.client?.id !== request.clientId)
        return create(RevokeMyAuthorizedClientResponseSchema, {
          status: 'success',
          message: 'the consent was withdrawn'
        })
      }
    })
  })

  const screen = await render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transport}>
        <AuthorizedAppsView />
      </TransportProvider>
    </QueryClientProvider>
  )
  revoked = undefined
  return screen
}

describe('Authorized apps (browser)', () => {
  it('lists the consents with their client, scopes, and last use', async () => {
    const screen = await renderApps([
      consent('gallery', 'Gallery', ['openid', 'offline_access']),
      consent('notes', 'Notes', ['openid', 'login_hint'])
    ])
    await expect.element(screen.getByText('Gallery')).toBeVisible()
    await expect.element(screen.getByText('Notes')).toBeVisible()
    await expect.element(screen.getByText(/offline_access — persistent access/)).toBeVisible()
    await expect.element(screen.getByText(/login_hint — prefills the sign-in form/)).toBeVisible()
    await expect.element(screen.getByText(/last used/).nth(0)).toBeVisible()
  })

  it('shows the empty as the fact when nothing is authorized', async () => {
    const screen = await renderApps([])
    await expect.element(screen.getByText('No authorized apps')).toBeVisible()
  })

  it('withdraws a consent and its grants after the confirmation', async () => {
    const screen = await renderApps([consent('gallery', 'Gallery', ['openid'])])
    await expect.element(screen.getByText('Gallery')).toBeVisible()
    await screen.getByRole('button', { name: 'Withdraw' }).nth(0).click()
    const scope = screen.getByRole('alertdialog')
    await expect.element(scope).toBeVisible()
    const confirm = scope.getByRole('button', { name: 'Withdraw' }).element()
    if (!(confirm instanceof HTMLElement)) throw new Error('the confirm button is not an element')
    confirm.click()
    await expect.element(screen.getByText('No authorized apps')).toBeVisible()
    expect(revoked).toBe('gallery')
  })
})
