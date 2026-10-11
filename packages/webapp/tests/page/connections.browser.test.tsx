import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { AuthContext } from '#/hooks/use-auth'
import { ConnectionsView } from '#/routes/(app)/account/-connections-view'
import type {
  GetLinkedAccountTokensRequest,
  GetLinkedAccountTokensResponse,
  ListLinkedConnectionsResponse,
  UnlinkConnectionRequest
} from '~/codegen/authn_pb'
import {
  GetLinkedAccountTokensResponseSchema,
  ListLinkedConnectionsResponseSchema,
  OAuthLinkedAccountSchema,
  OAuthSSOService,
  UnlinkConnectionResponseSchema
} from '~/codegen/authn_pb'
import {
  type ReauthenticateRequest,
  ReauthenticateResponseSchema,
  type ReauthenticateResponse,
  SendReauthenticationCodeResponseSchema,
  WebAuthnService
} from '~/codegen/webauthn_pb'

function linkedAccount(id: string, provider: string, email: string) {
  return create(OAuthLinkedAccountSchema, {
    id,
    connectionId: 'oconn_x',
    provider,
    email,
    createdAt: { seconds: 1790000000n, nanos: 0 }
  })
}

const LINKED = [
  linkedAccount('olink_google', 'google', 'ada@example.com'),
  linkedAccount('olink_keycloak', 'keycloak', 'ada@example.com')
]

interface ConnectionHandlers {
  listLinkedConnections?: () => ListLinkedConnectionsResponse
  unlinkConnection?: (request: UnlinkConnectionRequest, requestHeader: Headers) => void
  getLinkedAccountTokens?: (
    request: GetLinkedAccountTokensRequest
  ) => GetLinkedAccountTokensResponse | undefined
  reauthenticate?: (request: ReauthenticateRequest) => ReauthenticateResponse
}

async function renderConnections(handlers?: ConnectionHandlers) {
  const reauthenticateHandler =
    handlers?.reauthenticate ??
    (() => create(ReauthenticateResponseSchema, { token: 'proof_from_modal' }))
  const transport = createRouterTransport(({ service }) => {
    service(OAuthSSOService, {
      listLinkedConnections:
        handlers?.listLinkedConnections ??
        (() =>
          create(ListLinkedConnectionsResponseSchema, {
            linkedAccounts: LINKED,
            status: 'success',
            message: 'the linked connections were read'
          })),
      unlinkConnection: (request, context) => {
        handlers?.unlinkConnection?.(request, context.requestHeader)
        return create(UnlinkConnectionResponseSchema, { status: 'success' })
      },
      getLinkedAccountTokens: (request) => {
        const override = handlers?.getLinkedAccountTokens?.(request)
        return (
          override ??
          create(GetLinkedAccountTokensResponseSchema, {
            accessToken: 'at_provider_9',
            refreshToken: 'rt_provider_9',
            expiresAt: { seconds: 1790003600n, nanos: 0 },
            connectionId: 'oconn_x',
            provider: 'google',
            status: 'success'
          })
        )
      }
    })
    service(WebAuthnService, {
      reauthenticate: (request) => reauthenticateHandler(request),
      sendReauthenticationCode: () => create(SendReauthenticationCodeResponseSchema, {})
    })
  })

  return render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transport}>
        <AuthContext.Provider
          value={{
            user: null,
            loggedIn: true,
            isLoading: false,
            login: async () => {
              throw new Error('sign in is not part of this page test')
            },
            continueSignIn: async () => {
              throw new Error('sign in is not part of this page test')
            },
            completeSignIn: async () => {
              throw new Error('sign in is not part of this page test')
            },
            verifyPasskeyLogin: async () => {
              throw new Error('sign in is not part of this page test')
            },
            requestOneTimeAccess: async () => {
              throw new Error('sign in is not part of this page test')
            },
            exchangeOneTimeToken: async () => {
              throw new Error('sign in is not part of this page test')
            },
            stopImpersonation: async () => {
              throw new Error('the impersonation banner is not part of this page test')
            },
            impersonateUser: async () => {
              throw new Error('impersonation is not part of this page test')
            },
            logout: () => {}
          }}
        >
          <ConnectionsView />
        </AuthContext.Provider>
      </TransportProvider>
    </QueryClientProvider>
  )
}

describe('Connections (browser)', () => {
  it('lists the linked providers with their address and binding instant', async () => {
    const screen = await renderConnections()
    await expect.element(screen.getByText('google')).toBeVisible()
    await expect.element(screen.getByText('keycloak')).toBeVisible()
    await expect.element(screen.getByText(/ada@example\.com/).nth(0)).toBeVisible()
    await expect.element(screen.getByText(/linked/).nth(0)).toBeVisible()
  })

  it('shows the empty as the fact when no provider is linked', async () => {
    const screen = await renderConnections({
      listLinkedConnections: () =>
        create(ListLinkedConnectionsResponseSchema, {
          linkedAccounts: [],
          status: 'success',
          message: 'the linked connections were read'
        })
    })
    await expect.element(screen.getByText('No linked accounts')).toBeVisible()
  })

  it('unlinks through the step-up: the proof is minted by the modal and spent on the call', async () => {
    const spentHeaders: string[] = []
    const unlinkedIds: string[] = []
    const screen = await renderConnections({
      reauthenticate: () => create(ReauthenticateResponseSchema, { token: 'proof_7' }),
      unlinkConnection: (request, requestHeader) => {
        spentHeaders.push(requestHeader.get('x-saka-reauthentication') ?? '')
        unlinkedIds.push(request.linkedAccountId)
      }
    })
    await expect.element(screen.getByText('google')).toBeVisible()
    await screen.getByRole('button', { name: 'Unlink' }).nth(0).click()
    const dialog = screen.getByRole('alertdialog')
    await expect.element(dialog).toBeVisible()
    await dialog.getByLabelText('Account password').fill('correct-horse-battery')
    const confirm = dialog.getByRole('button', { name: 'Confirm' }).element()
    if (!(confirm instanceof HTMLElement)) throw new Error('the confirm button is not an element')
    confirm.click()
    await vi.waitFor(() => {
      expect(unlinkedIds).toEqual(['olink_google'])
      expect(spentHeaders).toEqual(['proof_7'])
    })
  })

  it('opens the provider tokens for one read, the expiry included', async () => {
    const openedFor: string[] = []
    const screen = await renderConnections({
      getLinkedAccountTokens: (request) => {
        openedFor.push(request.linkedAccountId)
      }
    })
    await expect.element(screen.getByText('google')).toBeVisible()
    await screen.getByRole('button', { name: 'Tokens' }).nth(0).click()
    const dialog = screen.getByRole('dialog')
    await expect.element(dialog.getByText('google tokens')).toBeVisible()
    await expect.element(dialog.getByText('at_provider_9')).toBeVisible()
    await expect.element(dialog.getByText('rt_provider_9')).toBeVisible()
    await vi.waitFor(() => {
      expect(openedFor).toEqual(['olink_google'])
    })
  })

  it('answers the empty as the fact when the provider granted no tokens', async () => {
    const screen = await renderConnections({
      getLinkedAccountTokens: () =>
        create(GetLinkedAccountTokensResponseSchema, {
          accessToken: '',
          refreshToken: '',
          connectionId: 'oconn_x',
          provider: 'keycloak',
          status: 'success'
        })
    })
    await expect.element(screen.getByText('keycloak')).toBeVisible()
    await screen.getByRole('button', { name: 'Tokens' }).nth(1).click()
    const dialog = screen.getByRole('dialog')
    await expect
      .element(dialog.getByText('(none — the provider answered no tokens)').nth(0))
      .toBeVisible()
  })
})
