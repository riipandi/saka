import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vite-plus/test'
import {
  decodeListMetadata,
  listPageInput,
  parseListPage,
  parseSortBy,
  parseSortOrder,
  usePaginatedList,
  LIST_DEFAULT_LIMIT,
  LIST_MAX_LIMIT
} from '#/hooks/use-pagination'
import { AuditLogService } from '~/codegen/auditlog_pb'
import { ListMetadataSchema } from '~/codegen/common_pb'

function wrapperWith(transport: ReturnType<typeof createRouterTransport>) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>{children}</TransportProvider>
    </QueryClientProvider>
  )
}

describe('the list search params', () => {
  describe('parseListPage', () => {
    it('reads a one-based page number', () => {
      expect(parseListPage('3')).toBe(3)
      expect(parseListPage(7)).toBe(7)
    })

    it('refuses malformed and non-positive values', () => {
      expect(parseListPage(undefined)).toBeUndefined()
      expect(parseListPage('abc')).toBeUndefined()
      expect(parseListPage('0')).toBeUndefined()
      expect(parseListPage('-2')).toBeUndefined()
      expect(parseListPage('2.5')).toBeUndefined()
      expect(parseListPage({})).toBeUndefined()
    })
  })

  describe('parseSortOrder', () => {
    it("reads the wire's two orders only", () => {
      expect(parseSortOrder('asc')).toBe('asc')
      expect(parseSortOrder('desc')).toBe('desc')
      expect(parseSortOrder('up')).toBeUndefined()
      expect(parseSortOrder(undefined)).toBeUndefined()
    })
  })

  describe('parseSortBy', () => {
    it('reads a whitelisted column and falls back otherwise', () => {
      const allowed = ['event', 'created_at']
      expect(parseSortBy('event', allowed, 'created_at')).toBe('event')
      expect(parseSortBy('ip_address', allowed, 'created_at')).toBe('created_at')
      expect(parseSortBy(undefined, allowed, 'created_at')).toBe('created_at')
    })
  })
})

describe('listPageInput', () => {
  it('answers explicit page values so one page is one query key', () => {
    expect(listPageInput()).toEqual({ page: 1, limit: LIST_DEFAULT_LIMIT })
    expect(listPageInput(3)).toEqual({ page: 3, limit: LIST_DEFAULT_LIMIT })
    expect(listPageInput(3, 50)).toEqual({ page: 3, limit: 50 })
  })

  it("clamps the page size to the wire's own bounds", () => {
    expect(listPageInput(1, 0)).toEqual({ page: 1, limit: 1 })
    expect(listPageInput(1, 5000)).toEqual({ page: 1, limit: LIST_MAX_LIMIT })
  })

  it('re-anchors a non-positive page at the first', () => {
    expect(listPageInput(0)).toEqual({ page: 1, limit: LIST_DEFAULT_LIMIT })
  })
})

describe('decodeListMetadata', () => {
  it('decodes a full block', () => {
    const metadata = create(ListMetadataSchema, {
      page: 2,
      limit: 20,
      totalPages: 5,
      totalItems: 97,
      firstItemIndex: 20,
      lastItemIndex: 39
    })
    expect(decodeListMetadata(metadata)).toEqual({
      page: 2,
      limit: 20,
      totalPages: 5,
      totalItems: 97,
      firstItemIndex: 20,
      lastItemIndex: 39,
      hasPrevious: true,
      hasNext: true
    })
  })

  it('reads absent totals as unknown, never as a lie', () => {
    const metadata = create(ListMetadataSchema, { page: 1, limit: 20 })
    const summary = decodeListMetadata(metadata)
    expect(summary).toEqual({
      page: 1,
      limit: 20,
      totalPages: null,
      totalItems: null,
      firstItemIndex: null,
      lastItemIndex: null,
      hasPrevious: false,
      hasNext: null
    })
  })

  it('answers null when the response carries no block', () => {
    expect(decodeListMetadata(undefined)).toBeNull()
  })
})

describe('usePaginatedList', () => {
  it('serves the page the input named, with its summary', async () => {
    const list = vi.fn((request: { page?: number; limit?: number }) => ({
      logs: [{ event: 'sign_in' }],
      metadata: create(ListMetadataSchema, {
        page: request.page,
        limit: request.limit,
        totalPages: 3,
        totalItems: 55,
        firstItemIndex: 20,
        lastItemIndex: 39
      })
    }))
    const transport = createRouterTransport(({ service }) => {
      service(AuditLogService, { list })
    })

    const { result } = renderHook(
      () => usePaginatedList(AuditLogService.method.list, listPageInput(2)),
      { wrapper: wrapperWith(transport) }
    )
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.logs).toHaveLength(1)
    expect(result.current.summary).toEqual({
      page: 2,
      limit: 20,
      totalPages: 3,
      totalItems: 55,
      firstItemIndex: 20,
      lastItemIndex: 39,
      hasPrevious: true,
      hasNext: true
    })
    expect(list).toHaveBeenCalledWith(
      expect.objectContaining({ page: 2, limit: 20 }),
      expect.anything()
    )
  })

  it('answers a null summary when the response carries no metadata', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(AuditLogService, { list: () => ({ logs: [] }) })
    })

    const { result } = renderHook(
      () => usePaginatedList(AuditLogService.method.list, listPageInput()),
      { wrapper: wrapperWith(transport) }
    )
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.summary).toBeNull()
  })
})
