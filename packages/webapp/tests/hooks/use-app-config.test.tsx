import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { prefetchAppConfig, useAppConfig } from '#/hooks/use-app-config'
import { fetcher } from '#/libraries/api-client'
import { clearAuth, setAuthUser } from '#/libraries/guard/auth-store'

vi.mock('#/libraries/api-client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('#/libraries/api-client')>()),
  fetcher: vi.fn()
}))

const account = {
  id: 'user_v1_langdon',
  username: 'rlangdon',
  email: 'robert.langdon@example.com',
  displayName: 'Robert Langdon'
}

function wrapper(client?: QueryClient): (props: { children: ReactNode }) => ReactNode {
  const queryClient = client ?? new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return ({ children }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

describe('useAppConfig', () => {
  beforeEach(() => {
    vi.mocked(fetcher).mockResolvedValue({
      status: 'success',
      data: { oidc: { enabled: true } }
    })
    clearAuth()
  })

  afterEach(() => {
    vi.mocked(fetcher).mockReset()
    clearAuth()
  })

  it('serves the parsed document', async () => {
    const { result } = renderHook(() => useAppConfig(), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.oidc.enabled).toBe(true)
  })

  it('refetches when the signed-in account changes', async () => {
    const { result } = renderHook(() => useAppConfig(), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(vi.mocked(fetcher)).toHaveBeenCalledTimes(1)

    setAuthUser(account)
    await waitFor(() => expect(vi.mocked(fetcher)).toHaveBeenCalledTimes(2))
  })

  it('does not refetch while the account stays the same', async () => {
    const { result } = renderHook(() => useAppConfig(), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    setAuthUser(account)
    await waitFor(() => expect(vi.mocked(fetcher)).toHaveBeenCalledTimes(2))

    setAuthUser(account)
    await new Promise((resolve) => setTimeout(resolve, 25))
    expect(vi.mocked(fetcher)).toHaveBeenCalledTimes(2)
  })

  it('never refetches on its own — the document is immutable for the process', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { result, unmount } = renderHook(() => useAppConfig(), { wrapper: wrapper(queryClient) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(vi.mocked(fetcher)).toHaveBeenCalledTimes(1)

    // A remount far past any staleness window serves the same copy.
    unmount()
    const second = renderHook(() => useAppConfig(), { wrapper: wrapper(queryClient) })
    await waitFor(() => expect(second.result.current.isSuccess).toBe(true))
    expect(vi.mocked(fetcher)).toHaveBeenCalledTimes(1)
  })

  it('the prefetch warms the exact key the hook reads', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

    await prefetchAppConfig(queryClient)
    expect(vi.mocked(fetcher)).toHaveBeenCalledTimes(1)

    const { result } = renderHook(() => useAppConfig(), { wrapper: wrapper(queryClient) })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(vi.mocked(fetcher)).toHaveBeenCalledTimes(1)
    expect(result.current.data?.oidc.enabled).toBe(true)
  })
})
