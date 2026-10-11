import { createFileRoute } from '@tanstack/react-router'
import { parseListPage, parseSortOrder } from '#/hooks/use-pagination'
import { AdminAuditView } from './-audit-view'

/** The columns the wire's own `sort_by` whitelist answers. */
const SORT_COLUMNS = ['event', 'username', 'ip_address', 'created_at'] as const

export const Route = createFileRoute('/(app)/admin/audit')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    // Every key answers optional, so a plain navigation never owes the
    // list's state — absence is the first page, unfiltered, newest first.
    page: parseListPage(search.page),
    event:
      typeof search.event === 'string' && search.event.trim() ? search.event.trim() : undefined,
    userId:
      typeof search.userId === 'string' && search.userId.trim() ? search.userId.trim() : undefined,
    search:
      typeof search.search === 'string' && search.search.trim() ? search.search.trim() : undefined,
    sortBy:
      typeof search.sortBy === 'string' &&
      (SORT_COLUMNS as readonly string[]).includes(search.sortBy)
        ? search.sortBy
        : undefined,
    sortOrder: parseSortOrder(search.sortOrder)
  }),
  staticData: {
    pageTitle: 'Audit trail'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <AdminAuditView
      page={search.page}
      event={search.event}
      userId={search.userId}
      search={search.search}
      sortBy={search.sortBy}
      sortOrder={search.sortOrder}
      onFilterChange={(filters) => {
        // A new filter starts the list again: page three of another filter
        // is a page the caller did not choose.
        void navigate({
          search: {
            page: undefined,
            event: filters.event,
            userId: filters.userId,
            search: filters.search,
            sortBy: search.sortBy,
            sortOrder: search.sortOrder
          }
        })
      }}
      onSortChange={(sortBy, sortOrder) => {
        void navigate({
          search: {
            page: undefined,
            event: search.event,
            userId: search.userId,
            search: search.search,
            sortBy,
            sortOrder
          }
        })
      }}
      onPageChange={(next) => {
        void navigate({
          search: {
            page: next,
            event: search.event,
            userId: search.userId,
            search: search.search,
            sortBy: search.sortBy,
            sortOrder: search.sortOrder
          }
        })
      }}
    />
  )
}
