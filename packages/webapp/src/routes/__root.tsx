import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AnyRouteMatch } from '@tanstack/react-router'
import { Outlet, createRootRouteWithContext, useMatches } from '@tanstack/react-router'
import { UIProvider } from 'uilibs/components/base/provider'
import { ThemeProvider } from 'uilibs/theme'
import { rpcTransport } from '#/libraries/api-client'
import { CookiesProvider } from '#/libraries/cookies'
import { AuthProvider } from '#/libraries/guard/auth-provider'
import { TelemetryProvider } from '#/libraries/telemetry/telemetry-provider'
import { GlobalNotFound, GlobalError } from './-boundaries'
import DevTools from './-devtools'

export interface GlobalContext {
  queryClient: QueryClient
}

export type BreadcrumbValue = string | string[] | ((match: AnyRouteMatch) => string | string[])

export const Route = createRootRouteWithContext<GlobalContext>()({
  notFoundComponent: GlobalNotFound,
  errorComponent: GlobalError,
  component: RootComponent,
  loader({ context }) {
    return { ...context }
  }
})

function RootComponent() {
  const matches = useMatches()
  const { queryClient } = Route.useRouteContext()
  const pageTitle = matches.findLast((match) => match.staticData?.pageTitle)?.staticData?.pageTitle

  return (
    <CookiesProvider defaultSetOptions={{ path: '/' }}>
      <title>{pageTitle ? `${pageTitle} - React Application` : 'React Application'}</title>
      <UIProvider direction='ltr'>
        <QueryClientProvider client={queryClient}>
          <TransportProvider transport={rpcTransport}>
            <AuthProvider>
              <ThemeProvider disableTransitionOnChange>
                <Outlet />
              </ThemeProvider>
            </AuthProvider>
            <DevTools queryClient={queryClient} />
            <TelemetryProvider />
          </TransportProvider>
        </QueryClientProvider>
      </UIProvider>
    </CookiesProvider>
  )
}
