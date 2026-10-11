import { createFileRoute } from '@tanstack/react-router'
import { OidcClientDetailView } from './-oidc-client-detail-view'

export const Route = createFileRoute('/(app)/admin/oidc-clients/$id')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'OIDC client'
  }
})

function RouteComponent() {
  const { id } = Route.useParams()
  return <OidcClientDetailView clientId={id} />
}
