import { useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query'
import { useEffect } from 'react'
import { readAppConfig, type AppConfig } from '#/libraries/app-config'
import { authStore } from '#/libraries/guard/auth-store'

export const APP_CONFIG_QUERY_KEY = ['app-config']

/**
 * The deployment document. Refetched when the signed-in account changes —
 * the document's scope widens for an administrator.
 */
export function useAppConfig(): UseQueryResult<AppConfig> {
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: APP_CONFIG_QUERY_KEY, queryFn: readAppConfig })

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
