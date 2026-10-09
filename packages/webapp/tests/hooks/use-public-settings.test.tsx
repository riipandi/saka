import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vite-plus/test'
import { usePublicSettings } from '#/hooks/use-public-settings'
import { SettingsService } from '~/codegen/settings_pb'

function wrapperWith(transport: ReturnType<typeof createRouterTransport>) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>{children}</TransportProvider>
    </QueryClientProvider>
  )
}

describe('usePublicSettings', () => {
  it("serves the deployment's public toggles", async () => {
    const listPublic = vi.fn(() => ({
      settings: [
        { key: 'users.self_delete_enabled', value: 'true' },
        { key: 'users.change_email_enabled', value: 'true' },
        { key: 'users.change_username_enabled', value: 'false' }
      ]
    }))
    const transport = createRouterTransport(({ service }) => {
      service(SettingsService, { listPublic })
    })

    const { result } = renderHook(() => usePublicSettings(), { wrapper: wrapperWith(transport) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.isOn('users.self_delete_enabled')).toBe(true)
    expect(result.current.isOn('users.change_email_enabled')).toBe(true)
    expect(result.current.isOn('users.change_username_enabled')).toBe(false)
    expect(result.current.get('users.self_delete_enabled')).toBe('true')
    expect(result.current.get('unknown.key')).toBeUndefined()
    expect(result.current.isOn('unknown.key')).toBe(false)
  })

  it('reads a gate closed while the read is still in flight', () => {
    const transport = createRouterTransport(({ service }) => {
      service(SettingsService, {
        listPublic: () => new Promise(() => {}) // never answers
      })
    })

    const { result } = renderHook(() => usePublicSettings(), { wrapper: wrapperWith(transport) })
    expect(result.current.isOn('users.self_delete_enabled')).toBe(false)
    expect(result.current.get('users.self_delete_enabled')).toBeUndefined()
  })
})
