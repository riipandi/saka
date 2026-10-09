import { createFileRoute } from '@tanstack/react-router'
import { parseListPage } from '#/hooks/use-pagination'
import { SessionsView } from '#/routes/(app)/account/-sessions-view'

export const Route = createFileRoute('/(app)/account/sessions')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    page: parseListPage(search.page)
  }),
  staticData: {
    pageTitle: 'Sessions'
  }
})

/** The route owns the search params; the view owns the data and the actions. */
function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <SessionsView
      page={search.page}
      onPageChange={(next) => {
        void navigate({
          search: (prev: { page?: number }) => ({ ...prev, page: next })
        })
      }}
    />
  )
}
