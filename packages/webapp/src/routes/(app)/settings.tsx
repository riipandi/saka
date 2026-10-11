import { createFileRoute, redirect } from '@tanstack/react-router'

/** The old `/settings` path moved under the account settings shell as the Profile tab. */
export const Route = createFileRoute('/(app)/settings')({
  beforeLoad: () => {
    throw redirect({ to: '/account/profile', replace: true })
  },
  staticData: { pageTitle: 'Settings' }
})
