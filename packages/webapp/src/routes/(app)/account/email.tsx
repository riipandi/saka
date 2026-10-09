import { createFileRoute } from '@tanstack/react-router'
import { EmailView } from './-email-view'

export const Route = createFileRoute('/(app)/account/email')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Email'
  }
})

function RouteComponent() {
  return <EmailView />
}
