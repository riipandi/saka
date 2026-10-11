import { createFileRoute } from '@tanstack/react-router'
import { parseListPage } from '#/hooks/use-pagination'
import { QueueView } from './-queue-view'

export const Route = createFileRoute('/(app)/admin/ops/queue')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    page: parseListPage(search.page),
    deadPage: parseListPage(search.deadPage),
    tab:
      search.tab === 'dead'
        ? ('dead' as const)
        : search.tab === 'pending'
          ? ('pending' as const)
          : undefined
  }),
  staticData: {
    pageTitle: 'Task queues'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <QueueView
      page={search.page}
      deadPage={search.deadPage}
      tab={search.tab}
      onSearchChange={(next) => {
        void navigate({ search: (prev) => ({ ...prev, ...next }) })
      }}
    />
  )
}
