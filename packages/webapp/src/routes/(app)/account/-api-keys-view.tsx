import { timestampDate, timestampFromDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation } from '@connectrpc/connect-query'
import { Plus } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger
} from 'uilibs/components/base/dropdown-menu'
import { Tooltip, TooltipContent, TooltipTrigger } from 'uilibs/components/base/tooltip'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from 'uilibs/components/extra/table'
import { Text } from 'uilibs/components/extra/text'
import { TableSkeleton } from '#/components/-skeleton-rows'
import { ShowOnceSecretDialog } from '#/components/dialogs/-show-once-secret-dialog'
import { ListPagination } from '#/components/list-pagination'
import { TimeValue } from '#/components/time-value'
import { usePaginatedList } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import { ApiKeyService } from '~/codegen/api_key_pb'
import { AccountFrame } from './-account-frame'
import { ApiKeyCreateSheet } from './-api-key-sheet'

/** The RFC 3339 instants the key view carries, read as the viewer's own
 * wall clock — a key list is recognized by when it lives, not by the zone
 * the server wrote. */
/** The key's one-line state: revoked dominates, then expired, then live. */
function keyState(
  expiresAt?: Timestamp | null,
  revokedAt?: Timestamp | null
): {
  word: string
  color: 'neutral' | 'critical' | 'success'
} {
  if (revokedAt) return { word: 'revoked', color: 'neutral' }
  if (expiresAt && timestampDate(expiresAt).getTime() < Date.now()) {
    return { word: 'expired', color: 'critical' }
  }
  return { word: 'active', color: 'success' }
}

/**
 * The account's own API keys (Wave 5 phase 5): the list the caller owns,
 * the create dialog whose minted raw key is shown once, renewal of an
 * expired key — the one operation that re-mints — and revocation, which
 * is a stamp rather than a deletion.
 */
export function ApiKeysView({
  page,
  onPageChange,
  embedded = false
}: {
  page?: number
  onPageChange: (page: number) => void
  embedded?: boolean
}) {
  const queryClient = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false)
  const [mintedKey, setMintedKey] = useState<string | null>(null)
  const [renewal, setRenewal] = useState<{ id: string; key: string } | null>(null)

  const keys = usePaginatedList(ApiKeyService.method.listAPIKeys, { page: page ?? 1, limit: 20 })

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: ApiKeyService.method.listAPIKeys,
        cardinality: 'finite'
      })
    })
  }

  const renew = useMutation(ApiKeyService.method.renewAPIKey, {
    onSuccess: (response) => {
      setRenewal({ id: response.apiKey?.id ?? '', key: response.key })
      invalidate()
    }
  })
  const revoke = useMutation(ApiKeyService.method.revokeAPIKey, { onSuccess: invalidate })

  return (
    <AccountFrame embedded={embedded}>
      <div {...stylex.props(pageStyles.stack)}>
        {keys.isPending ? (
          <TableSkeleton rows={5} />
        ) : keys.isError ? (
          <Text variant='body-2' color='critical'>
            {getErrorMessage(keys.error)}
          </Text>
        ) : (keys.data?.apiKeys ?? []).length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No keys yet</EmptyTitle>
              <EmptyDescription>
                A key lets a script or service call the API as this account. Issue one when a task
                needs it, and revoke it when the task is done.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <>
            <Card>
              <CardHeader>
                <CardTitle>Personal access tokens</CardTitle>
                <CardDescription>
                  Machine credentials that act as this account. The raw key is shown once at
                  creation and at renewal — only its hash is stored.
                </CardDescription>
                <CardAction>
                  <Button onClick={() => setCreateOpen(true)}>
                    <Plus size={16} /> New key
                  </Button>
                </CardAction>
              </CardHeader>
              <CardContent>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Key prefix</TableHead>
                      <TableHead>Created</TableHead>
                      <TableHead>Expires</TableHead>
                      <TableHead>Last used</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>Actions</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(keys.data?.apiKeys ?? []).map((key) => {
                      const state = keyState(key.expiresAt, key.revokedAt)
                      const expired = state.word === 'expired'
                      return (
                        <TableRow key={key.id}>
                          <TableCell>{key.name}</TableCell>
                          <TableCell>
                            <Tooltip>
                              <TooltipTrigger {...stylex.props(styles.mono)}>
                                {key.prefix}…
                              </TooltipTrigger>
                              <TooltipContent>{key.id}</TooltipContent>
                            </Tooltip>
                          </TableCell>
                          <TableCell>
                            <TimeValue value={key.createdAt} />
                          </TableCell>
                          <TableCell>
                            <TimeValue value={key.expiresAt} />
                          </TableCell>
                          <TableCell>
                            {key.lastUsedAt ? <TimeValue value={key.lastUsedAt} /> : 'Never'}
                          </TableCell>
                          <TableCell>
                            <Badge color={state.color}>{state.word}</Badge>
                          </TableCell>
                          <TableCell>
                            <DropdownMenu>
                              <DropdownMenuTrigger render={<Button variant='outline' size='sm' />}>
                                Actions
                              </DropdownMenuTrigger>
                              <DropdownMenuContent align='end'>
                                <DropdownMenuItem
                                  onClick={() => void navigator.clipboard.writeText(key.id)}
                                >
                                  Copy ID
                                </DropdownMenuItem>
                                {expired && !key.revokedAt ? (
                                  <DropdownMenuItem
                                    disabled={renew.isPending}
                                    onClick={() => {
                                      const window = new Date()
                                      window.setMonth(window.getMonth() + 3)
                                      renew.mutate({
                                        id: key.id,
                                        expiresAt: timestampFromDate(window)
                                      })
                                    }}
                                  >
                                    Renew
                                  </DropdownMenuItem>
                                ) : null}
                                {!key.revokedAt ? (
                                  <DropdownMenuItem
                                    variant='destructive'
                                    disabled={revoke.isPending}
                                    onClick={() => revoke.mutate({ id: key.id })}
                                  >
                                    Revoke
                                  </DropdownMenuItem>
                                ) : null}
                              </DropdownMenuContent>
                            </DropdownMenu>
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              </CardContent>
              {keys.summary !== null && (keys.summary.totalPages ?? 1) > 1 && (
                <CardFooter>
                  <ListPagination
                    page={keys.summary.page}
                    totalPages={keys.summary.totalPages}
                    hasPrevious={keys.summary.hasPrevious}
                    hasNext={keys.summary.hasNext}
                    onPageChange={onPageChange}
                  />
                </CardFooter>
              )}
            </Card>
          </>
        )}
      </div>

      <ApiKeyCreateSheet
        open={createOpen}
        onOpenChange={(next) => {
          setCreateOpen(next)
          if (!next) setCreateOpen(false)
        }}
        onMinted={(raw) => {
          setCreateOpen(false)
          setMintedKey(raw)
          invalidate()
        }}
      />

      <ShowOnceSecretDialog
        open={mintedKey !== null}
        onOpenChange={(next) => {
          if (!next) setMintedKey(null)
        }}
        title='The raw key'
        description='It authenticates as this account. It is shown once — store it now; losing it means revoking the key and issuing another.'
        secret={mintedKey ?? ''}
        secretLabel='API key'
      />

      <ShowOnceSecretDialog
        open={renewal !== null}
        onOpenChange={(next) => {
          if (!next) setRenewal(null)
        }}
        title='The renewed key'
        description='The old secret is dead from this moment. The new raw key is shown once — store it now.'
        secret={renewal?.key ?? ''}
        secretLabel='API key'
      />
    </AccountFrame>
  )
}

const styles = stylex.create({
  mono: {
    fontFamily: 'var(--font-mono, monospace)'
  },
  pager: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 12,
    width: '100%'
  }
})
