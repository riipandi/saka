import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { AuditView } from '#/routes/(app)/account/-audit-view'
import {
  AuditLogSchema,
  AuditLogService,
  ListResponseSchema,
  type AuditLog,
  type ListResponse
} from '~/codegen/auditlog_pb'

type AuditLogOverrides = Partial<Omit<AuditLog, '$typeName' | '$unknown'>>

function record(id: string, event: string, overrides: AuditLogOverrides = {}): AuditLog {
  return create(AuditLogSchema, {
    id,
    createdAt: { seconds: BigInt(1727769600), nanos: 0 },
    event,
    triggerType: 'user',
    actionStatus: 'success',
    username: 'hermione',
    ipAddress: '10.0.0.9',
    ...overrides
  })
}

const RECORDS: AuditLog[] = [
  record('log_1', 'sign_in'),
  record('log_2', 'account_updated', { actionStatus: 'failed' }),
  record('log_3', 'account_updated', { actorUsername: 'albus', ipAddress: '' })
]

function pageResponse(page: number, logs: AuditLog[]): ListResponse {
  return create(ListResponseSchema, {
    logs,
    metadata: {
      page,
      limit: 20,
      totalPages: 3,
      totalItems: 45,
      firstItemIndex: (page - 1) * 20,
      lastItemIndex: page * 20
    },
    status: 'success'
  })
}

async function renderAudit(options: {
  page?: number
  response?: ListResponse
  onPageChange?: (next: number) => void
  onSortChange?: (sortBy: string | undefined, sortOrder: 'asc' | 'desc' | undefined) => void
}) {
  const list = vi.fn(
    (request: { page?: number }) => options.response ?? pageResponse(request.page ?? 1, RECORDS)
  )
  const transport = createRouterTransport(({ service }) => {
    service(AuditLogService, { list })
  })

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const screen = await render(
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>
        <AuditView
          page={options.page}
          onPageChange={options.onPageChange}
          onSortChange={options.onSortChange}
        />
      </TransportProvider>
    </QueryClientProvider>
  )
  return { screen, list }
}

describe('Audit trail (browser)', () => {
  it('renders the records as sentences with their outcomes', async () => {
    const { screen } = await renderAudit({})
    await expect.element(screen.getByText('Signed in')).toBeVisible()
    await expect.element(screen.getByText('Account updated').first()).toBeVisible()
    expect(screen.baseElement.textContent).toContain('hermione')
    expect(screen.baseElement.textContent).toContain('from 10.0.0.9')
    expect(screen.baseElement.textContent).toContain('by albus (delegated)')
    await expect.element(screen.getByText('Failed')).toBeVisible()
    expect(screen.baseElement.textContent).toContain('Success')
  })

  it('answers an empty trail with the empty state', async () => {
    const { screen } = await renderAudit({ response: pageResponse(1, []) })
    await expect.element(screen.getByText('No activity yet')).toBeVisible()
  })

  it('turns the pages the summary offers', async () => {
    const onPageChange = vi.fn()
    const { screen } = await renderAudit({ page: 2, onPageChange })
    await expect.element(screen.getByText('21–40 of 45')).toBeVisible()

    await screen.getByRole('button', { name: 'Next' }).click()
    expect(onPageChange).toHaveBeenCalledWith(3)
  })

  it("orders the page through the route's sort state", async () => {
    const onSortChange = vi.fn()
    const { screen } = await renderAudit({ onSortChange })

    await screen.getByText('Newest first').click()
    const choice = screen.getByRole('option', { name: /Event, A/ })
    await expect.element(choice).toBeVisible()
    await choice.click()
    await vi.waitFor(() => {
      expect(onSortChange).toHaveBeenCalledWith('event', 'asc')
    })
  })
})
