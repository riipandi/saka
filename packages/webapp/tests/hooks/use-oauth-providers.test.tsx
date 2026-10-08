import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vite-plus/test'
import { useOAuthProviders } from '#/hooks/use-oauth-providers'
import { OAuthSSOService } from '~/codegen/authn_pb'

function wrapperWith(transport: ReturnType<typeof createRouterTransport>) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
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
})
