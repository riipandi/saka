import { createFileRoute } from '@tanstack/react-router'
import { parseListPage } from '#/hooks/use-pagination'
import { SignupTokensView } from './-signup-tokens-view'

export const Route = createFileRoute('/(app)/admin/signup-tokens')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    page: parseListPage(search.page)
  }),
  staticData: {
    pageTitle: 'Signup tokens'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <SignupTokensView
      page={search.page}
      onPageChange={(next) => {
        void navigate({ search: { page: next } })
      }}
    />
  )
}
