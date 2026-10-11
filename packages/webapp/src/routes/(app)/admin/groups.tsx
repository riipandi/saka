import { createFileRoute } from '@tanstack/react-router'
import { parseListPage, parseSortOrder } from '#/hooks/use-pagination'
import { GroupsView } from './-groups-view'

/** The columns the wire's own `sort_by` whitelist answers. */
const SORT_COLUMNS = ['name', 'display_name', 'user_count', 'created_at'] as const

export const Route = createFileRoute('/(app)/admin/groups')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    // Every key answers optional, so a plain navigation never owes the
    // list's state — absence is the first page in the wire's own order.
    page: parseListPage(search.page),
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
    pageTitle: 'Groups'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <GroupsView
      page={search.page}
      search={search.search}
      sortBy={search.sortBy}
      sortOrder={search.sortOrder}
      onSearchChange={(term) => {
        void navigate({
          search: {
            page: undefined,
            search: term,
            sortBy: search.sortBy,
            sortOrder: search.sortOrder
          }
        })
      }}
      onSortChange={(sortBy, sortOrder) => {
        void navigate({
          search: { page: undefined, search: search.search, sortBy, sortOrder }
        })
      }}
      onPageChange={(next) => {
        void navigate({
          search: {
            page: next,
            search: search.search,
            sortBy: search.sortBy,
            sortOrder: search.sortOrder
          }
        })
      }}
    />
  )
}
