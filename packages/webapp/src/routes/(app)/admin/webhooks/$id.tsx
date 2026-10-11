import { createFileRoute } from '@tanstack/react-router'
import { WebhookDetailView } from './-webhook-detail-view'

export const Route = createFileRoute('/(app)/admin/webhooks/$id')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Webhook endpoint'
  }
})

function RouteComponent() {
  const { id } = Route.useParams()
  return <WebhookDetailView webhookId={id} />
}
