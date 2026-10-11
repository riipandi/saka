import { createFileRoute } from '@tanstack/react-router'
import { parseListPage } from '#/hooks/use-pagination'
import { SchedulerView } from './-scheduler-view'

export const Route = createFileRoute('/(app)/admin/ops/scheduler')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    page: parseListPage(search.page)
  }),
  staticData: {
    pageTitle: 'Scheduler'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <SchedulerView
      page={search.page}
      onPageChange={(next) => {
        void navigate({
          search: (prev: { page?: number }) => ({ ...prev, page: next })
        })
      }}
    />
  )
}
