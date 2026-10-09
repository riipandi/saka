import { createFileRoute, redirect } from '@tanstack/react-router'
import { ensureSessionLoaded } from '#/libraries/guard/auth-session'
import { isAuthenticated } from '#/libraries/guard/auth-store'

export const Route = createFileRoute('/')({
  beforeLoad: async () => {
    // The root answers the browser once: a signed-in visitor goes
    // to the dashboard, an anonymous one to the sign-in screen.
    await ensureSessionLoaded()
    if (isAuthenticated()) {
      throw redirect({ to: '/overview' })
    }
    throw redirect({ to: '/login' })
  },
  staticData: { pageTitle: 'Home' }
})
