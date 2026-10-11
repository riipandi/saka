import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger
} from 'uilibs/components/base/alert-dialog'
import { Button } from 'uilibs/components/base/button'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
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
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { ListPagination } from '#/components/list-pagination'
import { listPageInput, decodeListMetadata } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import { NotificationService } from '~/codegen/notification_pb'
import { NotificationBroadcastDialog } from './-notification-dialog'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The broadcast console (Wave 5 phase 2): every notification the
 * deployment holds — live and cancelled — paged newest first, with the
 * publish dialog and the withdrawal the ledger keeps.
 */
export function NotificationsView() {
  const [page, setPage] = useState(1)
  const [composeOpen, setComposeOpen] = useState(false)
  const queryClient = useQueryClient()

  const list = useQuery(NotificationService.method.listAllNotifications, listPageInput(page), {
    retry: false
  })
  const cancel = useMutation(NotificationService.method.cancelNotification, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: NotificationService.method.listAllNotifications,
          cardinality: 'finite'
        })
      })
    }
  })

  const rows = list.data?.notifications ?? []
  const summary = decodeListMetadata(list.data?.metadata)

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
            Administration
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            Notifications
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            What the deployment announces to its accounts. A publish is live the moment the call
            returns; an audience is resolved at read time, never fanned out at create.
          </Text>
        </div>
        <div {...stylex.props(styles.headerActions)}>
          <Button onClick={() => setComposeOpen(true)}>New broadcast</Button>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Every notification</CardTitle>
            <CardDescription>
              Live and cancelled rows, newest first — a cancellation withdraws the notice from every
              inbox and the row stays, named by its cancellation instant.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {list.isPending ? (
              <Spinner />
            ) : list.isError ? (
              <Text variant='body-2' color='critical'>
                {getErrorMessage(list.error)}
              </Text>
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>Nothing published yet</EmptyTitle>
                  <EmptyDescription>
                    The first announcement goes live the moment its creation returns.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <>
                <ItemGroup>
                  {rows.map((notice) => (
                    <Item key={notice.id}>
                      <ItemContent>
                        <ItemTitle>{notice.title}</ItemTitle>
                        <ItemDescription>
                          {notice.category} · {notice.audienceKind}
                          {notice.topic ? ` · ${notice.topic}` : ''} · live{' '}
                          {formatInstant(notice.createdAt)}
                          {notice.cancelledAt
                            ? ` · cancelled ${formatInstant(notice.cancelledAt)}`
                            : ''}
                          {notice.emailSent ? ' · emailed' : ''}
                        </ItemDescription>
                      </ItemContent>
                      <ItemActions>
                        {notice.cancelledAt ? (
                          <Badge color='neutral'>cancelled</Badge>
                        ) : (
                          <AlertDialog>
                            <AlertDialogTrigger
                              render={
                                <Button
                                  variant='outline'
                                  size='sm'
                                  disabled={cancel.isPending}
                                  aria-label={`Cancel ${notice.title}`}
                                />
                              }
                            >
                              Cancel
                            </AlertDialogTrigger>
                            <CancelConfirm
                              title={notice.title}
                              onConfirm={() => cancel.mutate({ id: notice.id })}
                              pending={cancel.isPending}
                            />
                          </AlertDialog>
                        )}
                      </ItemActions>
                    </Item>
                  ))}
                </ItemGroup>
                {summary !== null && (summary.hasPrevious || summary.hasNext) ? (
                  <ListPagination
                    page={summary.page}
                    totalPages={summary.totalPages}
                    hasPrevious={summary.hasPrevious}
                    hasNext={summary.hasNext}
                    onPageChange={setPage}
                  />
                ) : null}
              </>
            )}
          </CardContent>
        </Card>
      </div>

      <NotificationBroadcastDialog open={composeOpen} onOpenChange={setComposeOpen} />
    </div>
  )
}

function CancelConfirm({
  title,
  onConfirm,
  pending
}: {
  title: string
  onConfirm: () => void
  pending: boolean
}) {
  return (
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>Cancel "{title}"?</AlertDialogTitle>
        <AlertDialogDescription>
          Every account stops seeing it and no email goes out for it afterwards. The row survives,
          named by its cancellation instant.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel>Keep</AlertDialogCancel>
        <AlertDialogAction disabled={pending} onClick={onConfirm}>
          {pending ? <Spinner /> : 'Cancel notification'}
        </AlertDialogAction>
      </AlertDialogFooter>
    </AlertDialogContent>
  )
}

const styles = stylex.create({
  pager: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'flex-end',
    gap: 12,
    marginTop: 16
  },
  headerActions: {
    display: 'flex',
    alignItems: 'center',
    gap: 10
  }
})
