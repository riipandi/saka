import { createFileRoute } from '@tanstack/react-router'
import { AccountNotificationsView } from './-notifications-view'

export const Route = createFileRoute('/(app)/account/notifications')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Notifications'
  }
})

function RouteComponent() {
  return <AccountNotificationsView />
}
