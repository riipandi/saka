import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderView } from './-placeholder-view'

export const Route = createFileRoute('/(app)/admin/users')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Users'
  }
})

function RouteComponent() {
  return <PlaceholderView page='Users' />
}
