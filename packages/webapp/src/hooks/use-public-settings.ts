import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useQuery } from '@connectrpc/connect-query'
import type { QueryClient, UseQueryResult } from '@tanstack/react-query'
import {
  ListPublicRequestSchema,
  type ListPublicResponse,
  SettingsService
} from '~/codegen/settings_pb'

/**
 * The public settings read: `SettingsService/ListPublic` — the catalog
 * items the backend flagged `public`, and nothing else. This is the
 * frontend's only capability source for feature toggles the deployment
 * turns by setting: the account screens gate their own actions by what
 * this answers, never by a frontend constant.
 *
 * The result carries two readers beside the query so a page never
 * re-implements the string decoding. `isOn` reads `false` while the query
 * is still in flight — a gate unknown is a gate closed, which keeps a slow
 * read from flashing an action the deployment forbids.
 */
export interface PublicSettingsReaders {
  /** The item's value as the wire carries it — a string — or undefined. */
  get: (key: string) => string | undefined
  /** The boolean toggles read `true` only from the exact word `true`. */
  isOn: (key: string) => boolean
}

export function usePublicSettings(): UseQueryResult<ListPublicResponse> & PublicSettingsReaders {
  const query = useQuery(SettingsService.method.listPublic, create(ListPublicRequestSchema))

  const values = new Map(query.data?.settings.map((setting) => [setting.key, setting.value]))
  const get = (key: string) => values.get(key)
  const isOn = (key: string) => values.get(key) === 'true'

  return { ...query, get, isOn }
}

/**
 * Drop the public settings from the cache — the same key the hook reads —
 * after a caller has reason to believe the deployment's answers moved. The
 * backend drops its own cache entry on every change; this is the client's
 * mirror of that.
 */
export function invalidatePublicSettings(queryClient: QueryClient): Promise<void> {
  return queryClient.invalidateQueries({
    queryKey: createConnectQueryKey({
      schema: SettingsService.method.listPublic,
      input: create(ListPublicRequestSchema),
      cardinality: 'finite'
    })
  })
}
