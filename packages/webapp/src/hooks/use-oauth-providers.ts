import { create } from '@bufbuild/protobuf'
import { createClient } from '@connectrpc/connect'
import { createConnectQueryKey, useQuery } from '@connectrpc/connect-query'
import type { QueryClient } from '@tanstack/react-query'
import { rpcTransport } from '#/libraries/api-client'
import { ListEnabledConnectionsRequestSchema, OAuthSSOService } from '~/codegen/authn_pb'

/**
 * The providers the deployment's connection store offers the sign-in page —
 * the slug and the display name of every enabled connection. The
 * configuration's `oauth.enabled` switch gates the surface on the backend;
 * the hook mirrors its document through `useAppConfig`.
 */
export function useOAuthProviders() {
  return useQuery(OAuthSSOService.method.listEnabledConnections)
}

/**
 * Warm the query before the login route renders. The key is the exact one
 * the hook generates — same method descriptor, same empty input, same
 * transport — so the prefetched copy is served, not refetched.
 */
export function prefetchOAuthProviders(
  queryClient: QueryClient,
  transport: typeof rpcTransport = rpcTransport
): Promise<void> {
  const input = create(ListEnabledConnectionsRequestSchema)
  return queryClient.prefetchQuery({
    queryKey: createConnectQueryKey({
      schema: OAuthSSOService.method.listEnabledConnections,
      input,
      transport,
      cardinality: 'finite'
    }),
    queryFn: () => createClient(OAuthSSOService, transport).listEnabledConnections(input)
  })
}
