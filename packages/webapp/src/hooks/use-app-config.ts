import {
  useQuery,
  useQueryClient,
  type QueryClient,
  type UseQueryResult
} from '@tanstack/react-query'
import { useEffect } from 'react'
import { readAppConfig, type AppConfig } from '#/libraries/app-config'
import { authStore } from '#/libraries/guard/auth-store'

export const APP_CONFIG_QUERY_KEY = ['app-config']

/**
 * The deployment document. Immutable for the life of the process — a change
 * is a server restart — so the query never refetches on its own; the account
 * flip below is the one invalidation, because the document's scope widens
 * for an administrator.
 */
export function useAppConfig(): UseQueryResult<AppConfig> {
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: APP_CONFIG_QUERY_KEY,
    queryFn: readAppConfig,
    staleTime: Infinity
  })

  useEffect(() => {
    let lastAccountId: string | null = authStore.state.user?.id ?? null
    const subscription = authStore.subscribe((state) => {
      const accountId = state.user?.id ?? null
      if (accountId === lastAccountId) return
      lastAccountId = accountId
      void queryClient.invalidateQueries({ queryKey: APP_CONFIG_QUERY_KEY })
    })
    return () => subscription.unsubscribe()
  }, [queryClient])

  return query
}

/**
 * Warm the query before the login route renders. Carries the same
 * `staleTime` the hook declares, so a prefetched copy is served, not
 * refetched.
 */
export function prefetchAppConfig(queryClient: QueryClient): Promise<void> {
  return queryClient.prefetchQuery({
    queryKey: APP_CONFIG_QUERY_KEY,
    queryFn: readAppConfig,
    staleTime: Infinity
  })
}
