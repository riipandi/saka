import { createFileRoute } from '@tanstack/react-router'
import { DeviceApprovalView } from './-device-view'

export const Route = createFileRoute('/(app)/device')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    user_code: typeof search.user_code === 'string' ? search.user_code : undefined
  }),
  staticData: {
    pageTitle: 'Device approval'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  return <DeviceApprovalView initialCode={search.user_code} />
}
