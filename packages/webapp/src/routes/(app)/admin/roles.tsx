import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderView } from './-placeholder-view'

export const Route = createFileRoute('/(app)/admin/roles')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Roles and permissions'
  }
})

function RouteComponent() {
  return <PlaceholderView page='Roles and permissions' />
}
