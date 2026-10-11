import * as stylex from '@stylexjs/stylex'
import { createFileRoute } from '@tanstack/react-router'
import { parseListPage } from '#/hooks/use-pagination'
import { ApiKeysView } from './-api-keys-view'
import { AuthorizedAppsView } from './-authorized-apps-view'
import { ConnectionsView } from './-connections-view'

const styles = stylex.create({
  stack: {
    display: 'flex',
    flexDirection: 'column',
    gap: 24
  }
})

export const Route = createFileRoute('/(app)/account/access')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    page: parseListPage(search.page)
  }),
  staticData: {
    pageTitle: 'Access'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <div {...stylex.props(styles.stack)}>
      <ConnectionsView embedded />
      <AuthorizedAppsView embedded />
      <ApiKeysView
        page={search.page}
        onPageChange={(next) => {
          void navigate({
            search: (prev: { page?: number }) => ({ ...prev, page: next })
          })
        }}
      />
    </div>
  )
}
