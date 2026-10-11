import { createFileRoute } from '@tanstack/react-router'
import { parseListPage } from '#/hooks/use-pagination'
import { BlocklistView } from './-blocklist-view'

export const Route = createFileRoute('/(app)/admin/blocklist')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    page: parseListPage(search.page)
  }),
  staticData: {
    pageTitle: 'Signup blocklist'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <BlocklistView
      page={search.page}
      onPageChange={(next) => {
        void navigate({ search: { page: next } })
      }}
    />
  )
}
