import { createFileRoute } from '@tanstack/react-router'
import { ConsentsView } from './-consents-view'

export const Route = createFileRoute('/(app)/admin/consents')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Consents'
  }
})

function RouteComponent() {
  return <ConsentsView />
}
