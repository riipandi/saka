import { createFileRoute, redirect } from '@tanstack/react-router'
import { ensureSessionLoaded } from '#/libraries/guard/auth-session'
import { isAuthenticated } from '#/libraries/guard/auth-store'
import { DeviceApprovalView } from './-device-view'

export const Route = createFileRoute('/device')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    user_code: typeof search.user_code === 'string' ? search.user_code : undefined
  }),
  beforeLoad: async ({ location }) => {
    await ensureSessionLoaded()
    if (!isAuthenticated()) {
      throw redirect({
        to: '/login',
        search: {
          return_to: location.href,
          unauthenticated: true
        }
      })
    }
  },
  staticData: {
    pageTitle: 'Device approval'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  return <DeviceApprovalView initialCode={search.user_code} />
}
