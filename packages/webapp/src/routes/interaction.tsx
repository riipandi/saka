import { createFileRoute, notFound, redirect, useSearch } from '@tanstack/react-router'
import { z } from 'zod'
import { ensureSessionLoaded } from '#/libraries/guard/auth-session'
import { isAuthenticated } from '#/libraries/guard/auth-store'
import { ConsentView, parseInteractionCallback } from './-consent-view'

/**
 * The interaction door the provider redirects the browser to: the URL
 * carries the callback the flow resumes at — the session id the screen
 * carries back once the consent question is answered. No app chrome
 * surrounds it; the page before it and after it belongs to the relying
 * party's flow.
 */
export const Route = createFileRoute('/interaction')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Authorize'
  },
  validateSearch: z.object({ callback: z.string().optional() }),
  beforeLoad: async ({ search }) => {
    await ensureSessionLoaded()
    // A callback of a shape the provider does not write — a foreign URL,
    // a wrong path, garbage, or none at all — is a not-found, never a
    // redirect target: the screen would otherwise bounce whatever it was
    // handed onward.
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
