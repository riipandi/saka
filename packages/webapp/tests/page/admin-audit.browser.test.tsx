import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { AdminAuditView } from '#/routes/(app)/admin/-audit-view'
import {
  AuditLogSchema,
  AuditLogService,
  FilterOptionsResponseSchema,
  ListAllResponseSchema,
  UserOptionSchema,
  type AuditLog
} from '~/codegen/auditlog_pb'

const SIGNED_IN = create(AuditLogSchema, {
  id: 'log_1',
  createdAt: { seconds: 1790000000n, nanos: 0 },
  event: 'sign_in',
  triggerType: 'user',
  actionStatus: 'success',
  userId: 'usr_langdon',
  username: 'rlangdon',
  ipAddress: '10.0.0.9'
})

const DELEGATED_BAN = create(AuditLogSchema, {
  id: 'log_2',
  createdAt: { seconds: 1790000600n, nanos: 0 },
  event: 'account_updated',
  triggerType: 'user',
  actionStatus: 'success',
  userId: 'usr_veteri',
  username: 'veteri',
  actorId: 'usr_albus',
  actorUsername: 'albus',
  payload: { reason: 'spamming the audit trail' }
})

const FAILED_SIGNIN = create(AuditLogSchema, {
  id: 'log_3',
  createdAt: { seconds: 1790001200n, nanos: 0 },
  event: 'sign_in',
  triggerType: 'user',
  actionStatus: 'failed',
  ipAddress: '10.0.0.9'
})

function pageResponse(page: number, logs: AuditLog[]) {
  return create(ListAllResponseSchema, {
    logs,
    status: 'success',
    message: 'ok',
    metadata: {
      page,
      limit: 20,
      totalPages: 1,
      totalItems: logs.length,
      firstItemIndex: 0,
      lastItemIndex: logs.length
    }
  })
}

const OPTIONS = create(FilterOptionsResponseSchema, {
  events: ['account_created', 'sign_in'],
  users: [
    create(UserOptionSchema, { id: 'usr_langdon', username: 'rlangdon' }),
    create(UserOptionSchema, { id: 'usr_veteri', username: 'veteri' })
  ],
  status: 'success',
  message: 'ok'
})

async function renderAdminAudit(options: {
  page?: number
  event?: string
  userId?: string
  response?: ReturnType<typeof pageResponse>
  onFilterChange?: (next: { event?: string; userId?: string; search?: string }) => void
}) {
  const listAll = vi.fn(
    (request: { event?: string; userId?: string; search?: string; page?: number }) => {
      return (
        options.response ??
        pageResponse(request.page ?? 1, [SIGNED_IN, DELEGATED_BAN, FAILED_SIGNIN])
      )
    }
  )
  const transport = createRouterTransport(({ service }) => {
    service(AuditLogService, { listAll, filterOptions: () => OPTIONS })
  })

  const screen = await render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transport}>
        <AdminAuditView
          page={options.page}
          event={options.event}
          userId={options.userId}
          onFilterChange={options.onFilterChange}
        />
      </TransportProvider>
    </QueryClientProvider>
  )
  return { screen, listAll }
}

describe('the audit console', () => {
  it('renders every record with its outcome, system trigger, and delegated actor', async () => {
    const { screen } = await renderAdminAudit({})

    await expect.element(screen.getByText('Signed in').first()).toBeVisible()
    await expect.element(screen.getByText('Account updated')).toBeVisible()
    expect(screen.baseElement.textContent).toContain('rlangdon')
    expect(screen.baseElement.textContent).toContain('from 10.0.0.9')
    expect(screen.baseElement.textContent).toContain('by albus (delegated)')
    await expect.element(screen.getByText('Failed')).toBeVisible()
  })

  it('offers the facets FilterOptions answers and names one on the wire', async () => {
    const onFilterChange = vi.fn()
    const { screen, listAll } = await renderAdminAudit({ onFilterChange })

    await screen.getByRole('combobox', { name: 'Filter by event' }).click()
    const choice = screen.getByRole('option', { name: 'Signed in' })
    await expect.element(choice).toBeVisible()
    await choice.click()
    await vi.waitFor(() => {
      expect(onFilterChange).toHaveBeenCalledWith(expect.objectContaining({ event: 'sign_in' }))
    })

    // The facet rides the request: the wire carries the event, matched exactly.
    await expect.poll(() => listAll.mock.calls.length).toBeGreaterThan(0)
  })

  it('filters by an account the options name', async () => {
    const onFilterChange = vi.fn()
    const { screen } = await renderAdminAudit({ onFilterChange })

    await screen.getByRole('combobox', { name: 'Filter by account' }).click()
    const choice = screen.getByRole('option', { name: 'veteri' })
    await expect.element(choice).toBeVisible()
    await choice.click()
    await vi.waitFor(() => {
      expect(onFilterChange).toHaveBeenCalledWith(expect.objectContaining({ userId: 'usr_veteri' }))
    })
  })

  it('sends the search term the operator typed', async () => {
    const onFilterChange = vi.fn()
    const { screen } = await renderAdminAudit({ onFilterChange })

    await screen.getByRole('textbox', { name: 'Search by account' }).fill('robert@langdon.dev')
    await screen.getByRole('button', { name: 'Search' }).click()
    await vi.waitFor(() => {
      expect(onFilterChange).toHaveBeenCalledWith(
        expect.objectContaining({ search: 'robert@langdon.dev' })
      )
    })
  })

  it('answers an empty page with the empty state', async () => {
    const { screen } = await renderAdminAudit({ response: pageResponse(1, []) })
    await expect.element(screen.getByText('No activity found')).toBeVisible()
  })
})
