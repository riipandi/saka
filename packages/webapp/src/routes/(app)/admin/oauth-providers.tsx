import { createFileRoute } from '@tanstack/react-router'
import { OAuthProvidersView } from './-oauth-providers-view'

export const Route = createFileRoute('/(app)/admin/oauth-providers')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'OAuth providers'
  }
})

function RouteComponent() {
  return <OAuthProvidersView />
}
