import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { useQuery } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { Button } from 'uilibs/components/base/button'
import { Badge } from 'uilibs/components/extra/badge'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { usePaginatedList } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
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
            <ItemGroup>
              {(keys.data?.apiKeys ?? []).map((key) => {
                const state = keyState(key.expiresAt, key.revokedAt)
                return (
                  <Item key={key.id}>
                    <ItemContent>
                      <ItemTitle>{key.name}</ItemTitle>
                      <ItemDescription>
                        <span {...stylex.props(styles.mono)}>{key.prefix}…</span>
                        {' · owner '}
                        <OwnerName ownerId={key.ownerId} />
                        {' · issued '}
                        {formatInstant(key.createdAt)}
                        {' · expires '}
                        {formatInstant(key.expiresAt)}
                        {key.lastUsedAt
                          ? ` · last used ${formatInstant(key.lastUsedAt)}`
                          : ' · never used'}
                        {key.revokedAt ? ` · revoked ${formatInstant(key.revokedAt)}` : ''}
                      </ItemDescription>
                    </ItemContent>
                    <ItemActions>
                      <Badge color={state.color}>{state.word}</Badge>
                    </ItemActions>
                  </Item>
                )
              })}
            </ItemGroup>
            {(() => {
              const summary = keys.summary
              if (summary === null || (summary.totalPages ?? 1) <= 1) return null
              return (
                <div {...stylex.props(styles.pager)}>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={!summary.hasPrevious}
                    onClick={() => onPageChange(summary.page - 1)}
                  >
                    Previous
                  </Button>
                  <Text variant='body-2' color='neutral-faded'>
                    Page {summary.page} of {summary.totalPages ?? '—'}
                  </Text>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={summary.hasNext === false}
                    onClick={() => onPageChange(summary.page + 1)}
                  >
                    Next
                  </Button>
                </div>
              )
            })()}
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
    justifyContent: 'flex-end',
    gap: 12,
    marginTop: 16
  }
})
