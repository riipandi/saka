import { createFileRoute } from '@tanstack/react-router'
import { ClaimsView } from './-claims-view'

export const Route = createFileRoute('/(app)/admin/claims')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Custom claims'
  }
})

function RouteComponent() {
  return <ClaimsView />
}
