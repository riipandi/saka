import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderView } from './-placeholder-view'

export const Route = createFileRoute('/(app)/admin/audit')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Audit trail'
  }
})

function RouteComponent() {
  return <PlaceholderView page='Audit trail' />
}
