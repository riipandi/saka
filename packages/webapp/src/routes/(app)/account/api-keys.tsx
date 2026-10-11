import { createFileRoute, redirect } from '@tanstack/react-router'

/** Personal API keys joined the Access tab of the account settings shell. */
export const Route = createFileRoute('/(app)/account/api-keys')({
  beforeLoad: () => {
    throw redirect({ to: '/account/access', search: () => ({ page: undefined }), replace: true })
  },
  staticData: { pageTitle: 'API keys' }
})
