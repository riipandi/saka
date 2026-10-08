import { useQuery } from '@connectrpc/connect-query'
import { OAuthSSOService } from '~/codegen/authn_pb'

/**
 * The providers the deployment's connection store offers the sign-in page —
 * the slug and the display name of every enabled connection. The
 * configuration's `oauth.enabled` switch gates the surface on the backend;
 * the hook mirrors its document through `useAppConfig`.
 */
export function useOAuthProviders() {
  return useQuery(OAuthSSOService.method.listEnabledConnections)
}
