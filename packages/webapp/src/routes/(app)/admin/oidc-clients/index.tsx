import { createFileRoute } from '@tanstack/react-router'
import { OidcClientsView } from './-oidc-clients-view'

export const Route = createFileRoute('/(app)/admin/oidc-clients/')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    page:
      typeof search.page === 'number' && Number.isInteger(search.page) && search.page > 0
        ? search.page
        : undefined,
    search:
      typeof search.search === 'string' && search.search.trim() ? search.search.trim() : undefined
  }),
  staticData: {
    pageTitle: 'OIDC clients'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <OidcClientsView
      page={search.page}
      search={search.search}
      onPageChange={(next) => {
        void navigate({ search: { ...search, page: next } })
      }}
      onSearchChange={(next) => {
        // A new search starts the list again: page three of another query
        // is a page the caller did not choose.
        void navigate({ search: { page: undefined, search: next } })
      }}
    />
  )
}
