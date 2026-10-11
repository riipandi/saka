import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { useQuery } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { Tooltip, TooltipContent, TooltipTrigger } from 'uilibs/components/base/tooltip'
import { Badge } from 'uilibs/components/extra/badge'
import { Card, CardContent, CardFooter } from 'uilibs/components/extra/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import { Spinner } from 'uilibs/components/extra/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from 'uilibs/components/extra/table'
import { Text } from 'uilibs/components/extra/text'
import { ListPagination } from '#/components/list-pagination'
import { usePaginatedList } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { dataTableStyles } from '#/styles/element/data-table.stylex'
import { pageStyles } from '#/styles/pages/page.stylex'
import { ApiKeyService } from '~/codegen/api_key_pb'
import { UserService } from '~/codegen/identity_pb'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/** The key's one-line state; the admin view is read-only so no action
 * column exists — revocation stays with the owner. */
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
 * The owner behind an oversight row: the display name the account answers
 * with, falling back to its id while the lookup is on its way or when it
 * fails — an oversight row names its key even when the account does not.
 */
function OwnerName({ ownerId }: { ownerId: string }) {
  const owner = useQuery(
    UserService.method.getUser,
    { id: ownerId },
    { enabled: ownerId !== '', retry: false }
  )
  const name = owner.data?.user?.displayName || owner.data?.user?.username
  return <>{name || ownerId || 'unknown'}</>
}

/**
 * The deployment-wide key overview (Wave 5 phase 5): every machine
 * credential the deployment holds, with whose key each row is. The
 * surface is read-only by design — the management procedures are the
 * owner's, and this page names rather than acts.
 */
export function AllApiKeysView({
  page,
  onPageChange
}: {
  page?: number
  onPageChange: (page: number) => void
}) {
  const keys = usePaginatedList(ApiKeyService.method.listAllAPIKeys, {
    page: page ?? 1,
    limit: 20
  })

  return (
    <div
      {...stylex.props(
        pageStyles.container,
        pageStyles.containerPadMedium,
        pageStyles.containerPadLarge,
        pageStyles.containerPadXLarge
      )}
    >
      <div {...stylex.props(pageStyles.header)}>
        <div {...stylex.props(pageStyles.headerLeft)}>
          <Text render={<p />} variant='body-2' weight='medium' color='primary'>
            API keys
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            Every machine credential
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            Every key the deployment holds, with whose account each acts as. Issuing, renewing, and
            revoking stay with each key's owner.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        {keys.isPending ? (
          <Spinner />
        ) : keys.isError ? (
          <Text variant='body-2' color='critical'>
            {getErrorMessage(keys.error)}
          </Text>
        ) : (keys.data?.apiKeys ?? []).length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No keys issued</EmptyTitle>
              <EmptyDescription>
                No account has issued an API key yet. Keys appear here the moment one is issued.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <>
            <Card>
              <CardContent>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Key prefix</TableHead>
                      <TableHead>Owner</TableHead>
                      <TableHead>Created</TableHead>
                      <TableHead>Expires</TableHead>
                      <TableHead>Last used</TableHead>
                      <TableHead>Status</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(keys.data?.apiKeys ?? []).map((key) => {
                      const state = keyState(key.expiresAt, key.revokedAt)
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
                            <OwnerName ownerId={key.ownerId} />
                          </TableCell>
                          <TableCell style={dataTableStyles.instant}>
                            {formatInstant(key.createdAt)}
                          </TableCell>
                          <TableCell style={dataTableStyles.instant}>
                            {formatInstant(key.expiresAt)}
                          </TableCell>
                          <TableCell style={dataTableStyles.instant}>
                            {key.lastUsedAt ? formatInstant(key.lastUsedAt) : 'Never'}
                          </TableCell>
                          <TableCell>
                            <Badge color={state.color}>{state.word}</Badge>
                            {key.revokedAt ? ` · ${formatInstant(key.revokedAt)}` : ''}
                          </TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              </CardContent>
              {keys.summary && (keys.summary.hasPrevious || keys.summary.hasNext) ? (
                <CardFooter>
                  <ListPagination
                    page={keys.summary.page}
                    totalPages={keys.summary.totalPages}
                    hasPrevious={keys.summary.hasPrevious}
                    hasNext={keys.summary.hasNext}
                    onPageChange={onPageChange}
                  />
                </CardFooter>
              ) : null}
            </Card>
          </>
        )}
      </div>
    </div>
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
