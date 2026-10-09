import type {
  DescMessage,
  DescMethodUnary,
  MessageInitShape,
  MessageShape
} from '@bufbuild/protobuf'
import type { ConnectError } from '@connectrpc/connect'
import type { UseQueryOptions } from '@connectrpc/connect-query'
import { useQuery } from '@connectrpc/connect-query'
import type { UseQueryResult } from '@tanstack/react-query'
import { useMemo } from 'react'
import type { ListMetadata } from '~/codegen/common_pb'

/**
 * The page size every list shares until a surface states its own. The
 * backend's validation caps the field at 100 — `buf.validate` on every
 * list request's `limit` — so the cap here mirrors the wire's, not a
 * frontend opinion.
 */
export const LIST_DEFAULT_LIMIT = 20
export const LIST_MAX_LIMIT = 100

/**
 * The page state a list route carries in its search params, as the route's
 * `validateSearch` hands it over. Absent means the first page.
 */
export interface ListSearch {
  page?: number
}

/**
 * The sort state a list route carries, beside its page. The `sort_by`
 * whitelist is the surface's own — each list names the columns it answers.
 */
export type SortOrder = 'asc' | 'desc'

/**
 * Read the route's `page` search param as a one-based page number. A
 * missing, malformed, or non-positive value answers undefined — the caller
 * then sees the first page, and the URL never carries a page it did not
 * mean.
 */
export function parseListPage(raw: unknown): number | undefined {
  if (typeof raw !== 'number' && typeof raw !== 'string') return undefined
  const parsed = Number(raw)
  if (!Number.isInteger(parsed) || parsed < 1) return undefined
  return parsed
}

/**
 * Read the route's `sort_order` search param. Anything the wire would
 * refuse answers undefined, and the caller keeps its own default.
 */
export function parseSortOrder(raw: unknown): SortOrder | undefined {
  return raw === 'asc' || raw === 'desc' ? raw : undefined
}

/**
 * Read the route's `sort_by` search param against the whitelist the list
 * names. A value outside the whitelist — or absent — answers the fallback,
 * so a hand-edited URL sorts by the list's own order rather than failing.
 */
export function parseSortBy(raw: unknown, allowed: readonly string[], fallback: string): string {
  return typeof raw === 'string' && allowed.includes(raw) ? raw : fallback
}

/**
 * The page block every list request carries. The values are always
 * explicit — the wire answers unset fields with the first default-sized
 * page, but an explicit request keeps one query key per page instead of
 * two for the same data.
 */
export function listPageInput(page?: number, limit?: number): { page: number; limit: number } {
  const wanted = parseListPage(page) ?? 1
  const size = Math.min(Math.max(limit ?? LIST_DEFAULT_LIMIT, 1), LIST_MAX_LIMIT)
  return { page: wanted, limit: size }
}

/**
 * The pagination view a list page renders: the page it is on, the size it
 * asked for, and the totals the response knew. A value the response left
 * unset reads as `null` — the totals are optional on the wire because the
 * backend may not know them — and `hasNext` reads `null` for the same
 * reason: without a total, the page cannot promise there is more.
 */
export interface PageSummary {
  page: number
  limit: number
  totalPages: number | null
  totalItems: number | null
  firstItemIndex: number | null
  lastItemIndex: number | null
  hasPrevious: boolean
  hasNext: boolean | null
}

/**
 * Decode the response's `ListMetadata` into the view the pager renders.
 * A response without the block — an older shape, or a call that does not
 * paginate — answers `null`, and the page renders no pager at all.
 */
export function decodeListMetadata(metadata: ListMetadata | undefined): PageSummary | null {
  if (!metadata) return null
  const page = metadata.page ?? 1
  const limit = metadata.limit ?? LIST_DEFAULT_LIMIT
  const totalPages = metadata.totalPages ?? null
  const totalItems = metadata.totalItems ?? null
  return {
    page,
    limit,
    totalPages,
    totalItems,
    firstItemIndex: metadata.firstItemIndex ?? null,
    lastItemIndex: metadata.lastItemIndex ?? null,
    hasPrevious: page > 1,
    hasNext: totalPages === null ? null : page < totalPages
  }
}

/**
 * Narrow a list response to the shape that carries the metadata block. The
 * predicate replaces an assertion — the response types carry `metadata` by
 * shape, but `MessageShape<O>` is only ever a descriptor at this call site,
 * so the generic cannot vouch for it and the narrowing does.
 */
function hasMetadata(data: unknown): data is { metadata?: ListMetadata | undefined } {
  return typeof data === 'object' && data !== null && 'metadata' in data
}

/**
 * A paginated list query: Connect-Query's result with the page summary the
 * pager renders beside it. The hook exists so a list page never re-derives
 * the summary from the metadata block — and never forgets to. The page
 * inputs ride `input` (see `listPageInput`), which Connect-Query folds into
 * the query key by itself, so page two is cached as page two.
 *
 * A response without the block answers a `null` summary rather than a lie.
 */
export function usePaginatedList<I extends DescMessage, O extends DescMessage>(
  schema: DescMethodUnary<I, O>,
  input: MessageInitShape<I>,
  options?: UseQueryOptions<O>
): UseQueryResult<MessageShape<O>, ConnectError> & { summary: PageSummary | null } {
  const query = useQuery(schema, input, options)
  const metadata = hasMetadata(query.data) ? query.data.metadata : undefined
  const summary = useMemo(() => decodeListMetadata(metadata), [metadata])
  return { ...query, summary }
}
