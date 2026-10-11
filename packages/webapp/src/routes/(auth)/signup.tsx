import { createFileRoute } from '@tanstack/react-router'
import { SignupView } from './-signup-view'

export const Route = createFileRoute('/(auth)/signup')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    token: typeof search.token === 'string' ? search.token : undefined
  }),
  staticData: {
    pageTitle: 'Sign Up'
  }
})

function RouteComponent() {
  const search = Route.useSearch()
  return <SignupView prefill={search.token} />
}
