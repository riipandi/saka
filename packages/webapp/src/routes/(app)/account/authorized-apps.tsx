import { createFileRoute, redirect } from '@tanstack/react-router'

/** Authorized apps joined the Access tab of the account settings shell. */
export const Route = createFileRoute('/(app)/account/authorized-apps')({
  beforeLoad: () => {
    throw redirect({ to: '/account/access', search: () => ({ page: undefined }), replace: true })
  },
  staticData: { pageTitle: 'Authorized apps' }
})
