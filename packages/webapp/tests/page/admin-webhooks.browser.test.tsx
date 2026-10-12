import { create } from '@bufbuild/protobuf'
import { TimestampSchema } from '@bufbuild/protobuf/wkt'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createRootRoute,
  createMemoryHistory,
  createRouter,
  RouterProvider
} from '@tanstack/react-router'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { setAuthGrants } from '#/libraries/guard/auth-store'
import { WebhookDetailView } from '#/routes/(app)/admin/webhooks/-webhook-detail-view'
import { WebhooksView } from '#/routes/(app)/admin/webhooks/-webhooks-view'
import {
  type Webhook,
  WebhookSchema,
  type WebhookDelivery,
  WebhookDeliverySchema,
  type WebhookDeliveryAttempt,
  WebhookDeliveryAttemptSchema,
  ListWebhooksResponseSchema,
  ListWebhookDeliveriesResponseSchema,
  ListWebhookEventTypesResponseSchema,
  GetWebhookResponseSchema,
  CreateWebhookResponseSchema,
  DeleteWebhookResponseSchema,
  RotateWebhookSecretResponseSchema,
  TestWebhookResponseSchema,
  WebhookService
} from '~/codegen/webhook_pb'

setAuthGrants({ roles: ['administrator'], permissions: [] })

const created = { seconds: 1790000000n, nanos: 0 }

function endpoint(
  id: string,
  name: string,
  overrides: Partial<Omit<Webhook, '$typeName' | '$unknown'>> = {}
): Webhook {
  return create(WebhookSchema, {
    id,
    name,
    endpoint: 'https://example.com/hooks',
    method: 'POST',
    headers: {},
    enabled: true,
    eventTypes: [],
    createdAt: create(TimestampSchema, created),
    ...overrides
  })
}

function attempt(
  overrides: Partial<Omit<WebhookDeliveryAttempt, '$typeName' | '$unknown'>> = {}
): WebhookDeliveryAttempt {
  return create(WebhookDeliveryAttemptSchema, {
    id: 'att_1',
    attemptNumber: 1,
    responseStatus: 200,
    durationMs: 42,
    response: {},
    createdAt: create(TimestampSchema, created),
    ...overrides
  })
}

function delivery(
  id: string,
  event: string,
  overrides: Partial<Omit<WebhookDelivery, '$typeName' | '$unknown'>> = {}
): WebhookDelivery {
  return create(WebhookDeliverySchema, {
    id,
    event,
    status: 'succeeded',
    attemptCount: 1,
    createdAt: create(TimestampSchema, created),
    latestAttempt: attempt(),
    ...overrides
  })
}

const ENDPOINTS = [
  endpoint('whk_1', 'billing-bridge', { eventTypes: ['user.created', 'user.updated'] }),
  endpoint('whk_2', 'silent-relay', { enabled: false })
]

let createdWebhook: Record<string, unknown> | undefined
let deletedId: string | undefined
let rotatedId: string | undefined
let testedId: string | undefined

function transportFor(deliveries: WebhookDelivery[]) {
  return createRouterTransport(({ service }) => {
    service(WebhookService, {
      list: () => create(ListWebhooksResponseSchema, { webhooks: ENDPOINTS }),
      get: () =>
        create(GetWebhookResponseSchema, {
          webhook: ENDPOINTS[0],
          status: 'success',
          message: 'the endpoint was read'
        }),
      create: (request) => {
        createdWebhook = { ...request }
        return create(CreateWebhookResponseSchema, {
          webhook: endpoint('whk_new', request.name),
          secret: 'whsec_1a2b3c4d5e6f7g8h9i0j',
          status: 'success',
          message: 'the endpoint was registered'
        })
      },
      delete: (request) => {
        deletedId = request.id
        return create(DeleteWebhookResponseSchema, {
          status: 'success',
          message: 'the endpoint was deleted'
        })
      },
      rotateSecret: (request) => {
        rotatedId = request.id
        return create(RotateWebhookSecretResponseSchema, {
          webhook: ENDPOINTS[0],
          secret: 'whsec_rotated_9876543210zyx',
          status: 'success',
          message: 'the signing secret was replaced'
        })
      },
      test: (request) => {
        testedId = request.id
        return create(TestWebhookResponseSchema, {
          status: 'success',
          message: 'the test delivery was queued'
        })
      },
      listDeliveries: () => create(ListWebhookDeliveriesResponseSchema, { deliveries }),
      listEventTypes: () =>
        create(ListWebhookEventTypesResponseSchema, {
          eventTypes: [
            { name: 'user.created', description: 'An account joined the deployment.' },
            { name: 'user.updated', description: 'An account field was rewritten.' }
          ]
        })
    })
  })
}

async function renderWith(node: React.ReactNode, deliveries: WebhookDelivery[] = []) {
  const transport = transportFor(deliveries)
  const screen: RenderResult = await render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transport}>
        <WithRouter>{node}</WithRouter>
      </TransportProvider>
    </QueryClientProvider>
  )
  createdWebhook = undefined
  deletedId = undefined
  rotatedId = undefined
  testedId = undefined
  return screen
}

function WithRouter({ children }: { children: React.ReactNode }) {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] })
  })
  return <RouterProvider router={router} />
}

describe('Admin webhooks (browser)', () => {
  it('lists the endpoints with their state badges', async () => {
    const screen = await renderWith(<WebhooksView />)
    await expect.element(screen.getByText('billing-bridge')).toBeVisible()
    await expect.element(screen.getByText('enabled').nth(0)).toBeVisible()
    await expect.element(screen.getByText('disabled').nth(0)).toBeVisible()
  })

  it('registers an endpoint and shows the minted secret once', async () => {
    const screen = await renderWith(<WebhooksView />)
    await screen.getByRole('button', { name: 'New endpoint' }).click()
    const dialog = screen.getByRole('dialog')
    await dialog.getByLabelText('Name').fill('ops-relay')
    await dialog.getByLabelText('Endpoint URL').fill('https://ops.example.com/hook')
    const register = dialog.getByRole('button', { name: 'Register' })
    await expect.element(register).toBeEnabled()
    const registerElement = register.element()
    if (!(registerElement instanceof HTMLElement))
      throw new Error('the register button is not an element')
    registerElement.click()
    const showOnce = screen.getByRole('dialog')
    await expect
      .element(showOnce.getByLabelText('Signing secret'))
      .toHaveValue('whsec_1a2b3c4d5e6f7g8h9i0j')
    expect(createdWebhook).toMatchObject({ name: 'ops-relay', method: 'POST' })
  })

  it('deletes an endpoint after the confirmation', async () => {
    const screen = await renderWith(<WebhooksView />)
    // The row actions live behind the row's "Actions" dropdown; the target
    // endpoint is the first row.
    await screen.getByRole('button', { name: 'Actions' }).first().click()
    await expect.element(screen.getByRole('menu')).toBeVisible()
    const trigger = screen.getByRole('menu').getByRole('button', { name: 'Delete billing-bridge' })
    await expect.element(trigger).toBeVisible()
    await trigger.click()
    const scope = screen.getByRole('alertdialog')
    await expect.element(scope).toBeVisible()
    const confirm = scope.getByRole('button', { name: 'Delete endpoint' }).element()
    if (!(confirm instanceof HTMLElement)) throw new Error('the confirm button is not an element')
    confirm.click()
    await vi.waitFor(() => {
      expect(deletedId).toBe('whk_1')
    })
  })

  it('sends a test event and rotates the secret from the detail view', async () => {
    const screen = await renderWith(<WebhookDetailView webhookId='whk_1' />, [
      delivery('dlv_1', 'webhook.test')
    ])
    const testButton = screen.getByRole('button', { name: 'Send test event' })
    await expect.element(testButton).toBeVisible()
    const testElement = testButton.element()
    if (!(testElement instanceof HTMLElement)) throw new Error('the test button is not an element')
    testElement.click()
    await vi.waitFor(() => {
      expect(testedId).toBe('whk_1')
    })
    await expect.element(screen.getByText('webhook.test')).toBeVisible()
    await expect.element(screen.getByText('succeeded').nth(0)).toBeVisible()
    // The attempt's response status is its own column, the raw code.
    await expect.element(screen.getByText('200')).toBeVisible()
  })

  it('presents the rotated secret once', async () => {
    const screen = await renderWith(<WebhookDetailView webhookId='whk_1' />)
    const rotateButton = screen.getByRole('button', { name: 'Rotate secret' })
    await expect.element(rotateButton).toBeVisible()
    const rotateElement = rotateButton.element()
    if (!(rotateElement instanceof HTMLElement))
      throw new Error('the rotate button is not an element')
    rotateElement.click()
    const showOnce = screen.getByRole('dialog')
    await expect
      .element(showOnce.getByLabelText('Signing secret'))
      .toHaveValue('whsec_rotated_9876543210zyx')
    expect(rotatedId).toBe('whk_1')
  })

  it('renders the delivery log with its attempt metadata', async () => {
    const screen = await renderWith(<WebhookDetailView webhookId='whk_1' />, [
      delivery('dlv_1', 'user.created'),
      delivery('dlv_2', 'user.updated', {
        status: 'failed',
        attemptCount: 3,
        latestAttempt: attempt({ responseStatus: undefined, error: 'connection refused' })
      })
    ])
    await expect.element(screen.getByText('user.created').nth(1)).toBeVisible()
    await expect.element(screen.getByText('failed').nth(0)).toBeVisible()
    await expect.element(screen.getByText(/connection refused/).nth(0)).toBeVisible()
  })
})
