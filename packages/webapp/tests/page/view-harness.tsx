import type { Transport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { render, type RenderResult } from 'vitest-browser-react'
import { AuthContext, type AuthContextValue } from '#/hooks/use-auth'

/**
 * The harness the page-view browser tests share: the provider nesting
 * (query client, Connect transport, the auth context) and the signed-in
 * stub whose every un-exercised act refuses. A page test wires only what
 * its view touches — the rest is the same refusal everywhere.
 */

/** The query client a page test answers to: no retries, one render asserted. */
export function testQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: 0 } }
  })
}

/** The refusal every un-exercised act answers with — the act's name in the sentence. */
const refuse = (act: string) => () => {
  throw new Error(`${act} is not part of this page test`)
}

/** The auth context a signed-in page rides — every act the page does not exercise throws. */
export function signedInAuth(overrides: Partial<AuthContextValue> = {}): AuthContextValue {
  return {
    user: null,
    loggedIn: true,
    isLoading: false,
    login: refuse('sign in'),
    continueSignIn: refuse('sign in'),
    completeSignIn: refuse('sign in'),
    verifyPasskeyLogin: refuse('sign in'),
    requestOneTimeAccess: refuse('sign in'),
    exchangeOneTimeToken: refuse('sign in'),
    stopImpersonation: refuse('the impersonation banner'),
    impersonateUser: refuse('impersonation'),
    logout: () => {},
    ...overrides
  }
}

/** Render a view under the providers it reads — only the ones the options name. */
export async function renderView(
  view: ReactNode,
  opts: { queryClient?: QueryClient; transport?: Transport; auth?: AuthContextValue } = {}
): Promise<RenderResult> {
  let node: ReactNode = view
  if (opts.auth) {
    node = <AuthContext.Provider value={opts.auth}>{node}</AuthContext.Provider>
  }
  if (opts.transport) {
    node = <TransportProvider transport={opts.transport}>{node}</TransportProvider>
  }
  return render(
    <QueryClientProvider client={opts.queryClient ?? testQueryClient()}>{node}</QueryClientProvider>
  )
}
