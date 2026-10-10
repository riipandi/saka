import { createFileRoute } from '@tanstack/react-router'
import { SecurityView } from './-security-view'

export const Route = createFileRoute('/(app)/account/security')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Security'
  }
})

function RouteComponent() {
  return <SecurityView />
}
