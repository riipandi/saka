import { createFileRoute } from '@tanstack/react-router'
import { OidcClientNewView } from './-oidc-client-new-view'

export const Route = createFileRoute('/(app)/admin/oidc-clients/new')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'New OIDC client'
  }
})

function RouteComponent() {
  return <OidcClientNewView />
}
