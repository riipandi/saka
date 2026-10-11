import { createFileRoute } from '@tanstack/react-router'
import { PlaceholderView } from './-placeholder-view'

export const Route = createFileRoute('/(app)/admin/signup-tokens')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Signup tokens'
  }
})

function RouteComponent() {
  return <PlaceholderView page='Signup tokens' />
}
