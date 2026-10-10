import { createFileRoute, useSearch } from '@tanstack/react-router'
import { z } from 'zod'
import { LoginView } from './-login-view'

export const Route = createFileRoute('/(auth)/login')({
  component: RouteComponent,
  validateSearch: z.object({
    loggedOut: z.coerce.boolean().optional(),
    unauthenticated: z.coerce.boolean().optional(),
    return_to: z.string().optional()
  }),
  staticData: {
    pageTitle: 'Sign In'
  }
})

function RouteComponent() {
  const { loggedOut, unauthenticated, return_to: returnTo } = useSearch({ from: Route.id })
  return <LoginView loggedOut={loggedOut} unauthenticated={unauthenticated} returnTo={returnTo} />
}
