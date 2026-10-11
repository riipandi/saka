import { createFileRoute } from '@tanstack/react-router'
import { ConnectionsView } from './-connections-view'

export const Route = createFileRoute('/(app)/account/connections')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Connected accounts'
  }
})

function RouteComponent() {
  return <ConnectionsView />
}
