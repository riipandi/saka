import { createFileRoute } from '@tanstack/react-router'
import { AuthorizedAppsView } from './-authorized-apps-view'

export const Route = createFileRoute('/(app)/account/authorized-apps')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Authorized apps'
  }
})

function RouteComponent() {
  return <AuthorizedAppsView />
}
