import { createFileRoute, redirect } from '@tanstack/react-router'

/** Connected accounts joined the Access tab of the account settings shell. */
export const Route = createFileRoute('/(app)/account/connections')({
  beforeLoad: () => {
    throw redirect({ to: '/account/access', search: () => ({ page: undefined }), replace: true })
  },
  staticData: { pageTitle: 'Connected accounts' }
})
