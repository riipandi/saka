import { createFileRoute, Outlet, redirect } from '@tanstack/react-router'
import { isAdministrator } from '#/libraries/guard/permissions-store'

export const Route = createFileRoute('/(app)/admin')({
  component: RouteComponent,
  beforeLoad: () => {
    // The snapshot is the mint-time truth the backend's own `Admin` rules
    // will judge again on every call — this guard only keeps a non-
    // administrator out of the console's screens; the procedures' refusals
    // stay the authority. The notice tells the redirected visit why.
    if (!isAdministrator()) {
      throw redirect({ to: '/overview', search: { notice: 'admin' }, replace: true })
    }
  },
  staticData: {
    pageTitle: 'Administration'
  }
})

function RouteComponent() {
  return <Outlet />
}
