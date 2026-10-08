import atoms from '@stylexjs/atoms'
import * as stylex from '@stylexjs/stylex'
import { createFileRoute, Outlet, redirect } from '@tanstack/react-router'
import { ThemeSwitcher } from 'uilibs/theme'
import { z } from 'zod'
import { prefetchAppConfig } from '#/hooks/use-app-config'
import { prefetchOAuthProviders } from '#/hooks/use-oauth-providers'
import { queryClient } from '#/libraries/api-client'
import { ensureSessionLoaded } from '#/libraries/guard/auth-session'
import { isAuthenticated } from '#/libraries/guard/auth-store'
import { safeReturnTo } from '#/libraries/guard/auth-utils'
import { styles } from '#/styles/element/auth-layout.stylex'

export const Route = createFileRoute('/(auth)')({
  component: RouteComponent,
  validateSearch: z.object({ return_to: z.string().optional() }).passthrough(),
  beforeLoad: async ({ search }) => {
    await ensureSessionLoaded()
    if (isAuthenticated()) {
      throw redirect({ href: safeReturnTo(search.return_to) ?? '/overview' })
    }
    // Warm the login page's two queries while the document renders — the
    // prefetches ride the same cache the hooks read, so a warm copy is
    // served instead of a mount-time fetch.
    void prefetchAppConfig(queryClient)
    void prefetchOAuthProviders(queryClient)
  }
})

function RouteComponent() {
  return (
    <main
      id='auth-layout'
      {...stylex.props(
        atoms.minHeight['100vh'],
        atoms.display.flex,
        atoms.alignItems.center,
        atoms.justifyContent.center,
        atoms.padding['1rem']
      )}
    >
      <header id='auth-header' {...stylex.props(styles.header)}>
        <ThemeSwitcher />
      </header>
      <div id='auth-card-wrapper' {...stylex.props(styles.wrapper)}>
        <Outlet />
      </div>
    </main>
  )
}
