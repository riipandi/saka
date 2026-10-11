import { createFileRoute } from '@tanstack/react-router'
import { SettingsView } from '#/routes/(app)/-settings-view'

export const Route = createFileRoute('/(app)/account/profile')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Profile'
  }
})

function RouteComponent() {
  return <SettingsView embedded />
}
