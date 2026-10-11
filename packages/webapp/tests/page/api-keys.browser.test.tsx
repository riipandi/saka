import { create } from '@bufbuild/protobuf'
import { timestampFromDate } from '@bufbuild/protobuf/wkt'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { setAuthGrants } from '#/libraries/guard/auth-store'
import { ApiKeysView } from '#/routes/(app)/account/-api-keys-view'
import { AllApiKeysView } from '#/routes/(app)/admin/-all-api-keys-view'
import {
  type ApiKey,
  ApiKeySchema,
  ListAPIKeysResponseSchema,
  ListAllAPIKeysResponseSchema,
  CreateAPIKeyResponseSchema,
  RenewAPIKeyResponseSchema,
  RevokeAPIKeyResponseSchema,
  ApiKeyService
} from '~/codegen/api_key_pb'
import { UserService } from '~/codegen/identity_pb'

setAuthGrants({ roles: ['administrator'], permissions: [] })

const future = new Date('2030-01-01T00:00:00Z')
const past = new Date('2020-01-01T00:00:00Z')

function key(
  id: string,
  name: string,
  overrides: Partial<Omit<ApiKey, '$typeName' | '$unknown'>> = {}
): ApiKey {
  return create(ApiKeySchema, {
    id,
    name,
    prefix: 'sak_' + id.slice(0, 4),
    ownerId: 'acc_1',
    createdAt: timestampFromDate(past),
    expiresAt: timestampFromDate(future),
    ...overrides
  })
}

let created: Record<string, unknown> | undefined
let renewedId: string | undefined
let revokedId: string | undefined

function transportFor(keys: ApiKey[]) {
  return createRouterTransport(({ service }) => {
    service(ApiKeyService, {
      listAPIKeys: () => create(ListAPIKeysResponseSchema, { apiKeys: keys }),
      listAllAPIKeys: () => create(ListAllAPIKeysResponseSchema, { apiKeys: keys }),
      createAPIKey: (request) => {
        created = { ...request }
        return create(CreateAPIKeyResponseSchema, {
          apiKey: key('key_new', request.name),
          key: 'sak_raw_1a2b3c4d5e6f7g8h9i0j'
        })
      },
      renewAPIKey: (request) => {
        renewedId = request.id
        return create(RenewAPIKeyResponseSchema, {
          apiKey: key(request.id, 'stale-job'),
          key: 'sak_raw_renewed_9876543210'
        })
      },
      revokeAPIKey: (request) => {
        revokedId = request.id
        return create(RevokeAPIKeyResponseSchema)
      }
    })
    service(UserService, {
      getUser: (request) => {
        throw new Error('no user lookup in this test: ' + request.id)
      }
    })
  })
}

async function renderWith(node: React.ReactNode, keys: ApiKey[] = []) {
  created = undefined
  renewedId = undefined
  revokedId = undefined
  return render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transportFor(keys)}>{node}</TransportProvider>
    </QueryClientProvider>
  )
}

describe('Account API keys (browser)', () => {
  it('lists own keys with their state badges', async () => {
    const screen = await renderWith(<ApiKeysView page={1} onPageChange={() => {}} />, [
      key('key_1', 'nightly-backup'),
      key('key_2', 'stale-job', { expiresAt: timestampFromDate(past) }),
      key('key_3', 'retired', { revokedAt: timestampFromDate(past) })
    ])
    await expect.element(screen.getByText('nightly-backup')).toBeVisible()
    await expect.element(screen.getByText('active').nth(0)).toBeVisible()
    await expect.element(screen.getByText('expired').nth(0)).toBeVisible()
    await expect.element(screen.getByText('revoked').nth(0)).toBeVisible()
  })

  it('issues a key and presents the raw key once', async () => {
    const screen = await renderWith(<ApiKeysView page={1} onPageChange={() => {}} />)
    await screen.getByRole('button', { name: 'New key' }).click()
    const dialog = screen.getByRole('dialog')
    await expect.element(dialog.getByLabelText('Name')).toBeVisible()
    await dialog.getByLabelText('Name').fill('deploy-bot')
    const issue = dialog.getByRole('button', { name: 'Issue key' })
    await expect.element(issue).toBeEnabled()
    const issueElement = issue.element()
    if (!(issueElement instanceof HTMLElement))
      throw new Error('the issue button is not an element')
    issueElement.click()
    const showOnce = screen.getByRole('dialog')
    await expect
      .element(showOnce.getByLabelText('API key'))
      .toHaveValue('sak_raw_1a2b3c4d5e6f7g8h9i0j')
    expect(created).toMatchObject({ name: 'deploy-bot' })
  })

  it('renews an expired key and shows the new raw key once', async () => {
    const screen = await renderWith(<ApiKeysView page={1} onPageChange={() => {}} />, [
      key('key_2', 'stale-job', { expiresAt: timestampFromDate(past) })
    ])
    const renewButton = screen.getByRole('button', { name: 'Renew' })
    await expect.element(renewButton).toBeVisible()
    const renewElement = renewButton.element()
    if (!(renewElement instanceof HTMLElement))
      throw new Error('the renew button is not an element')
    renewElement.click()
    const showOnce = screen.getByRole('dialog')
    await expect
      .element(showOnce.getByLabelText('API key'))
      .toHaveValue('sak_raw_renewed_9876543210')
    await vi.waitFor(() => {
      expect(renewedId).toBe('key_2')
    })
  })

  it('revokes an active key', async () => {
    const screen = await renderWith(<ApiKeysView page={1} onPageChange={() => {}} />, [
      key('key_1', 'nightly-backup')
    ])
    const revokeButton = screen.getByRole('button', { name: 'Revoke' })
    await expect.element(revokeButton).toBeVisible()
    const revokeElement = revokeButton.element()
    if (!(revokeElement instanceof HTMLElement))
      throw new Error('the revoke button is not an element')
    revokeElement.click()
    await vi.waitFor(() => {
      expect(revokedId).toBe('key_1')
    })
  })
})

describe('Admin API keys oversight (browser)', () => {
  it('lists every key read-only with its owner', async () => {
    const screen = await renderWith(<AllApiKeysView page={1} onPageChange={() => {}} />, [
      key('key_1', 'nightly-backup', { ownerId: 'acc_9' })
    ])
    await expect.element(screen.getByText('nightly-backup')).toBeVisible()
    await expect.element(screen.getByText(/owner acc_9/).nth(0)).toBeVisible()
    expect(screen.container.textContent).not.toContain('Revoke')
    expect(screen.container.textContent).not.toContain('Renew')
  })
})
