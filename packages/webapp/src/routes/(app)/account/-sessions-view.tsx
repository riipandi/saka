import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useMutation } from '@connectrpc/connect-query'
import { LaptopSmartphone } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { useAuthentication } from '#/hooks/use-auth'
import { listPageInput, usePaginatedList } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { SignOutDialog } from '#/routes/(app)/account/-sessions-dialog'
import { pageStyles } from '#/styles/pages/page.stylex'
import { styles } from '#/styles/pages/sessions.stylex'
import type { Session } from '~/codegen/authn_pb'
import {
  SessionService,
  SignOutAllSessionsRequestSchema,
  SignOutOtherSessionsRequestSchema
} from '~/codegen/authn_pb'

/**
 * The RFC 3339 instants the session view carries, read as the viewer's own
 * wall clock — a session list is recognized by when it was opened, not by
 * the zone the server wrote.
 */
const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value: string | undefined): string {
  if (!value) return '—'
  const instant = new Date(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/** The provider word the wire carries, read as a name. */
function formatProvider(provider: string): string {
  if (provider === 'password') return 'Password'
  if (provider === 'one_time_access') return 'One-time code'
  return provider
}

/**
 * The session center: the account's sessions, newest first, ended ones
 * included, the caller's own marked by the wire's `current` flag. The page
 * and the actions it takes are wired by the caller so the route owns the
 * search params and the view owns the data.
 *
 * The per-row revoke is deliberately absent — `RevokeSession` is step-up
 * guarded, and the reauthentication modal is Wave 2 machinery that this
 * page's action becomes its first consumer of. Until then the bulk
 * sign-outs, which carry no step-up, are the page's whole write surface.
 */
export function SessionsView({
  page = 1,
  onPageChange
}: {
  page?: number
  onPageChange?: (next: number) => void
}) {
  const queryClient = useQueryClient()
  const { logout } = useAuthentication()

  const [actionError, setActionError] = useState<string | null>(null)

  const sessions = usePaginatedList(SessionService.method.listSessions, listPageInput(page))

  const listKey = createConnectQueryKey({
    schema: SessionService.method.listSessions,
    input: listPageInput(page),
    cardinality: 'finite'
  })

  const signOutOthers = useMutation(SessionService.method.signOutOtherSessions, {
    onSuccess: () => {
      setActionError(null)
      void queryClient.invalidateQueries({ queryKey: listKey })
    },
    onError: (error) => setActionError(getErrorMessage(error))
  })

  // Sign-out-all ends the caller's own session too, so the pair is dropped
  // client-side with it (D6) — the engine's logout tolerates a failing
  // SignOut, and the shell's eviction effect carries the browser away.
  const signOutAll = useMutation(SessionService.method.signOutAllSessions, {
    onSuccess: () => logout(),
    onError: (error) => setActionError(getErrorMessage(error))
  })

  const rows = sessions.data?.sessions ?? []
  const summary = sessions.summary

  const turnPage = (next: number) => {
    onPageChange?.(next)
  }

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
          <Text
            render={<p />}
            variant='caption-1'
            weight='semibold'
            color='primary'
            style={pageStyles.kicker}
          >
            Account
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            Sessions
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The devices this account is signed in from, newest first.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Active sessions</CardTitle>
            <CardDescription>
              Ended sessions stay listed until they fall off the page.
            </CardDescription>
            <CardAction>
              <div {...stylex.props(styles.toolbarActions)}>
                <SignOutDialog
                  trigger='Sign out others'
                  title='Sign out other sessions?'
                  description='Every session but this one ends. The count is what the server answers, and zero is still a success.'
                  pending={signOutOthers.isPending}
                  error={signOutOthers.error ? getErrorMessage(signOutOthers.error) : null}
                  onConfirm={() => signOutOthers.mutate(create(SignOutOtherSessionsRequestSchema))}
                />
                <SignOutDialog
                  trigger='Sign out all'
                  title='Sign out everywhere?'
                  description='Every session ends — this one included. You will be signed out when it lands.'
                  pending={signOutAll.isPending}
                  error={signOutAll.error ? getErrorMessage(signOutAll.error) : null}
                  onConfirm={() => signOutAll.mutate(create(SignOutAllSessionsRequestSchema))}
                />
              </div>
            </CardAction>
          </CardHeader>
          <CardContent>
            {sessions.isPending ? (
              <Spinner />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No sessions</EmptyTitle>
                  <EmptyDescription>
                    This account holds no sessions — sign in to open one.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <ItemGroup>
                {rows.map((session) => (
                  <SessionItem key={session.id} session={session} />
                ))}
              </ItemGroup>
            )}

            {actionError ? (
              <Text render={<p />} variant='body-2' color='critical'>
                {actionError}
              </Text>
            ) : null}

            {summary && (summary.hasPrevious || summary.hasNext) ? (
              <div {...stylex.props(styles.pager)}>
                <Text variant='body-2' color='neutral-faded' style={styles.pagerStatus}>
                  {summary.totalItems === null
                    ? `Page ${summary.page}${summary.totalPages ? ` of ${summary.totalPages}` : ''}`
                    : `${(summary.firstItemIndex ?? 0) + 1}–${summary.lastItemIndex ?? ''} of ${summary.totalItems}`}
                </Text>
                <div {...stylex.props(styles.toolbarActions)}>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={!summary.hasPrevious}
                    onClick={() => turnPage(summary.page - 1)}
                  >
                    Previous
                  </Button>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={summary.hasNext === false}
                    onClick={() => turnPage(summary.page + 1)}
                  >
                    Next
                  </Button>
                </div>
              </div>
            ) : null}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

/** One session: the device it names, where and when it was opened, its state. */
function SessionItem({ session }: { session: Session }) {
  const live = !session.revokedAt
  return (
    <Item variant='outline'>
      <ItemMedia variant='icon'>
        <LaptopSmartphone {...stylex.props(styles.icon)} />
      </ItemMedia>
      <ItemContent>
        <ItemTitle>{session.userAgent ?? 'Unknown device'}</ItemTitle>
        <ItemDescription>
          {formatProvider(session.provider)}
          {session.ipAddress ? ` · from ${session.ipAddress}` : ''}
          {` · opened ${formatInstant(session.createdAt)}`}
          {` · expires ${formatInstant(session.expiresAt)}`}
        </ItemDescription>
      </ItemContent>
      <ItemActions>
        {session.current ? (
          <Badge variant='secondary'>This device</Badge>
        ) : live ? (
          <Badge variant='outline'>Live</Badge>
        ) : (
          <Badge variant='ghost'>Ended</Badge>
        )}
      </ItemActions>
    </Item>
  )
}
