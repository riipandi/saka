import { createFileRoute } from '@tanstack/react-router'
import { parseListPage, parseSortBy, parseSortOrder } from '#/hooks/use-pagination'
import { AuditView } from './-audit-view'

/** The columns the wire's own `sort_by` whitelist answers. */
const SORT_COLUMNS = ['event', 'username', 'ip_address', 'created_at'] as const

export const Route = createFileRoute('/(app)/account/audit')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    page: parseListPage(search.page),
    sortBy: parseSortBy(search.sortBy, SORT_COLUMNS, 'created_at'),
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
    <AuditView
      page={search.page}
      sortBy={search.sortBy}
      sortOrder={search.sortOrder}
      onPageChange={(next) => {
        void navigate({
          search: { page: next, sortBy: search.sortBy, sortOrder: search.sortOrder }
        })
      }}
      onSortChange={(sortBy, sortOrder) => {
        // A new order starts the list again: page three of another order is
        // a page the caller did not choose. The fallback names the column
        // the wire orders by when the choice carries none.
        void navigate({ search: { page: undefined, sortBy: sortBy ?? 'created_at', sortOrder } })
      }}
    />
  )
}
