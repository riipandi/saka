import { createFileRoute } from '@tanstack/react-router'
import { ResetPasswordView } from './-reset-password-view'

export const Route = createFileRoute('/(auth)/reset-password')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    token: typeof search.token === 'string' ? search.token : undefined
  }),
  staticData: {
    pageTitle: 'Reset Password'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  return <ResetPasswordView prefill={search.token} />
}
