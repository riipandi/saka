import { createFileRoute } from '@tanstack/react-router'
import { WebhooksView } from './-webhooks-view'

export const Route = createFileRoute('/(app)/admin/webhooks/')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Webhooks'
  }
})

function RouteComponent() {
  return <WebhooksView />
}
