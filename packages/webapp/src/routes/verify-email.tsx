import { createFileRoute } from '@tanstack/react-router'
import { VerifyEmailView } from './-verify-email-view'

export const Route = createFileRoute('/verify-email')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    code: typeof search.code === 'string' ? search.code : undefined
  }),
  staticData: {
    pageTitle: 'Verify email'
  }
})

function RouteComponent() {
  const { code } = Route.useSearch()
  return <VerifyEmailView initialCode={code} />
}
