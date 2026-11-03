import { createFileRoute, notFound, redirect, useSearch } from '@tanstack/react-router'
import { z } from 'zod'
import { ensureSessionLoaded } from '#/libraries/guard/auth-session'
import { isAuthenticated } from '#/libraries/guard/auth-store'
import { ConsentView, parseInteractionCallback } from './-consent-view'

export const Route = createFileRoute('/interaction')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Authorize'
  },
  validateSearch: z.object({ callback: z.string().optional() }),
  beforeLoad: async ({ search }) => {
    await ensureSessionLoaded()
    const callback = parseInteractionCallback(search.callback)
    if (!callback) throw notFound()
    if (!isAuthenticated()) {
      const returnTo = `/interaction?callback=${encodeURIComponent(callback)}`
      throw redirect({ href: `/login?return_to=${encodeURIComponent(returnTo)}` })
    }
  }
})

function RouteComponent() {
  const { callback } = useSearch({ from: Route.id })
  return <ConsentView callback={callback ?? ''} />
}
