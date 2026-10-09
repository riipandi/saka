import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vite-plus/test'
import { prefetchOAuthProviders, useOAuthProviders } from '#/hooks/use-oauth-providers'
import { OAuthSSOService } from '~/codegen/authn_pb'

function wrapperWith(transport: ReturnType<typeof createRouterTransport>, client?: QueryClient) {
  const queryClient = client ?? new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>{children}</TransportProvider>
    </QueryClientProvider>
  )
}

describe('useOAuthProviders', () => {
  it('serves the enabled connections the listing answers', async () => {
    const listEnabledConnections = vi.fn(() => ({
      connections: [
        { provider: 'google', displayName: 'Google' },
        { provider: 'hogwarts-sso', displayName: 'Hogwarts SSO' }
      ]
    }))
    const transport = createRouterTransport(({ service }) => {
      service(OAuthSSOService, { listEnabledConnections })
    })

    const { result } = renderHook(() => useOAuthProviders(), { wrapper: wrapperWith(transport) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.connections).toHaveLength(2)
    expect(result.current.data?.connections[0]?.provider).toBe('google')
    expect(result.current.data?.connections[1]?.displayName).toBe('Hogwarts SSO')
  })

  it('serves an empty listing when no connection is enabled', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(OAuthSSOService, { listEnabledConnections: () => ({ connections: [] }) })
    })

    const { result } = renderHook(() => useOAuthProviders(), { wrapper: wrapperWith(transport) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.connections).toHaveLength(0)
  })

  it('the prefetch warms the exact key the hook reads', async () => {
    const listEnabledConnections = vi.fn(() => ({
      connections: [{ provider: 'google', displayName: 'Google' }]
    }))
    const transport = createRouterTransport(({ service }) => {
      service(OAuthSSOService, { listEnabledConnections })
    })
    // The app's queryClient keeps a fetched copy fresh for five minutes; the
    // test mirrors that default, because the no-refetch guarantee is exactly
    // what the prefetch buys.
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 1000 * 60 * 5 } }
    })

    await prefetchOAuthProviders(queryClient, transport)
    expect(listEnabledConnections).toHaveBeenCalledTimes(1)

    // The hook consumes the prefetched copy — the transport never hears a
    // second call, which is only true when the keys match exactly.
    const { result } = renderHook(() => useOAuthProviders(), {
      wrapper: wrapperWith(transport, queryClient)
    })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(listEnabledConnections).toHaveBeenCalledTimes(1)
    expect(result.current.data?.connections[0]?.provider).toBe('google')
  })
})
