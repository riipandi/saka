import { createFileRoute } from '@tanstack/react-router'
import { AdminSettingsView } from './-settings-view'

export const Route = createFileRoute('/(app)/admin/settings')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Platform settings'
  }
})

function RouteComponent() {
  return <AdminSettingsView />
}
