import { createFileRoute } from '@tanstack/react-router'
import { parseListPage } from '#/hooks/use-pagination'
import { SessionsView } from './-sessions-view'

export const Route = createFileRoute('/(app)/account/sessions')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    page: parseListPage(search.page)
  }),
  staticData: {
    pageTitle: 'Sessions'
  }
})

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
