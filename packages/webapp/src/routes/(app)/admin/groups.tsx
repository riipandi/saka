import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderView } from './-placeholder-view'

export const Route = createFileRoute('/(app)/admin/groups')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Groups'
  }
})

function RouteComponent() {
  return <PlaceholderView page='Groups' />
}
