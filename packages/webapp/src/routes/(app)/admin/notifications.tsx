import { createFileRoute } from '@tanstack/react-router'
import { NotificationsView } from './-notifications-view'

export const Route = createFileRoute('/(app)/admin/notifications')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Notifications'
  }
})

function RouteComponent() {
  return <NotificationsView />
}
