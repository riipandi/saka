import { createFileRoute, useSearch } from '@tanstack/react-router'
import { z } from 'zod'
import { MfaPauseView } from './-mfa-pause-view'

export const Route = createFileRoute('/(auth)/login/mfa')({
  component: RouteComponent,
  // The pause page carries the login page's own search through — the
  // return_to, the one-shot notices, and any operator param ride along
  // (passthrough), so a refresh keeps the destination exactly as the fork
  // left it.
  validateSearch: z
    .object({
      loggedOut: z.coerce.boolean().optional(),
      unauthenticated: z.coerce.boolean().optional(),
      reset: z.coerce.boolean().optional(),
      return_to: z.string().optional()
    })
    .passthrough(),
  staticData: {
    pageTitle: 'Two-Factor Authentication'
  }
})
function RouteComponent() {
  const search = useSearch({ from: Route.id })
  return <MfaPauseView search={search} />
}
