import { createFileRoute } from '@tanstack/react-router'
import { SettingsView } from '#/routes/(app)/-settings-view'

export const Route = createFileRoute('/(app)/settings')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Settings'
  }
})

/** The route owns the wiring; the view owns the data and the actions. */
function RouteComponent() {
  return <SettingsView />
}
