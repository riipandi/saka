import { createFileRoute } from '@tanstack/react-router'
import { parseListPage } from '#/hooks/use-pagination'
import { ApiKeysView } from './-api-keys-view'

export const Route = createFileRoute('/(app)/account/api-keys')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    page: parseListPage(search.page)
  }),
  staticData: {
    pageTitle: 'API keys'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <ApiKeysView
      page={search.page}
      onPageChange={(next) => {
        void navigate({
          search: (prev: { page?: number }) => ({ ...prev, page: next })
        })
      }}
    />
  )
}
