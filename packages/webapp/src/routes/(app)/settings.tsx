import { createFileRoute } from '@tanstack/react-router'
import { SettingsView } from '#/routes/(app)/-settings-view'

export const Route = createFileRoute('/(app)/settings')({
  component: SettingsView,
  staticData: {
    pageTitle: 'Settings'
  }
})
