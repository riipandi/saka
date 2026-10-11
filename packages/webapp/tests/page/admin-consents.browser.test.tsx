import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { setAuthGrants } from '#/libraries/guard/auth-store'
import { ConsentsView } from '#/routes/(app)/admin/-consents-view'

setAuthGrants({
  roles: ['administrator'],
  permissions: ['oidc_consent:*:read', 'oidc_consent:*:list']
})
import {
  AuthorizedOidcClientEntrySchema,
  AuthorizedOidcClientSchema,
  ListAllAuthorizedClientsResponseSchema,
  ListUserAuthorizedClientsResponseSchema,
  OidcClientSchema,
  OidcConsentService
} from '~/codegen/federation_pb'
import { ListUsersResponseSchema, UserSchema, UserService } from '~/codegen/identity_pb'

const GALLERY = create(OidcClientSchema, { id: 'gallery', name: 'Gallery', callbackUrls: [] })
const NOTES = create(OidcClientSchema, { id: 'notes', name: 'Notes', callbackUrls: [] })

async function renderConsents() {
  const transport = createRouterTransport(({ service }) => {
    service(OidcConsentService, {
      listAllAuthorizedClients: () =>
        create(ListAllAuthorizedClientsResponseSchema, {
          entries: [
            create(AuthorizedOidcClientEntrySchema, {
              userId: 'usr_ada',
              client: GALLERY,
              scopes: ['openid', 'offline_access'],
              lastUsedAt: { seconds: 1790000000n, nanos: 0 }
            }),
            create(AuthorizedOidcClientEntrySchema, {
              userId: 'usr_grace',
              client: NOTES,
              scopes: ['openid', 'login_hint'],
              lastUsedAt: { seconds: 1790000000n, nanos: 0 }
            })
          ],
          metadata: { page: 1, limit: 20, totalPages: 1, totalItems: 2 },
          status: 'success',
          message: 'the ledger was read'
        }),
      listUserAuthorizedClients: () =>
        create(ListUserAuthorizedClientsResponseSchema, {
          clients: [
            create(AuthorizedOidcClientSchema, {
              client: GALLERY,
              scopes: ['openid', 'offline_access'],
              lastUsedAt: { seconds: 1790000000n, nanos: 0 }
            })
          ],
          status: 'success',
          message: 'the ledger was read'
        })
    })
    service(UserService, {
      listUsers: () =>
        create(ListUsersResponseSchema, {
          users: [
            create(UserSchema, { id: 'usr_ada', email: 'ada@example.com', displayName: 'Ada' }),
            create(UserSchema, {
              id: 'usr_grace',
              email: 'grace@example.com',
              displayName: 'Grace'
            })
          ],
          metadata: { page: 1, limit: 100, totalPages: 1, totalItems: 2 },
          status: 'success',
          message: 'the accounts were read'
        })
    })
  })

  return render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transport}>
        <ConsentsView />
      </TransportProvider>
    </QueryClientProvider>
  )
}

describe('Consents (browser)', () => {
  it('lists the deployment-wide ledger with the granting account named', async () => {
    const screen = await renderConsents()
    await expect.element(screen.getByText('Gallery')).toBeVisible()
    await expect.element(screen.getByText('Notes')).toBeVisible()
    await expect.element(screen.getByText(/account usr_ada/)).toBeVisible()
    await expect.element(screen.getByText(/account usr_grace/)).toBeVisible()
    await expect.element(screen.getByText(/offline_access — persistent access/)).toBeVisible()
    await expect.element(screen.getByText(/login_hint — prefills the sign-in form/)).toBeVisible()
  })

  it("reads one account's ledger whole when the account is picked", async () => {
    const screen = await renderConsents()
    await expect.element(screen.getByText('Gallery')).toBeVisible()
    await screen.getByRole('tab', { name: 'By account' }).click()
    await screen.getByRole('combobox').nth(0).click()
    await screen.getByRole('option', { name: 'Ada (ada@example.com)' }).click()
    await expect.element(screen.getByText("The account's consents")).toBeVisible()
    await expect.element(screen.getByText('Gallery')).toBeVisible()
  })
})
