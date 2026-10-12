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
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle
} from 'uilibs/components/extra/empty'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { ListError } from '#/components/-list-error'
import { PageHeader } from '#/components/-page-header'
import { TableSkeleton } from '#/components/-skeleton-rows'
import { ListPagination } from '#/components/list-pagination'
import { listPageInput, decodeListMetadata } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import { NotificationService } from '~/codegen/notification_pb'
import { NotificationBroadcastSheet } from './-notification-sheet'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The broadcast console (Wave 5 phase 2): every notification the
 * deployment holds — live and cancelled — paged newest first, with the
 * publish sheet and the withdrawal the ledger keeps.
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
      <PageHeader
        kicker='Administration'
        title='Notifications'
        description='What the deployment announces to its accounts. A publish is live the moment the call returns; an audience is resolved at read time, never fanned out at create.'
        actions={<Button onClick={() => setComposeOpen(true)}>New broadcast</Button>}
      />

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
              <TableSkeleton rows={6} />
            ) : list.isError ? (
              <ListError
                message={getErrorMessage(list.error)}
                onRetry={() => void list.refetch()}
              />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>Nothing published yet</EmptyTitle>
                  <EmptyDescription>
                    The first announcement goes live the moment its creation returns.
                  </EmptyDescription>
                </EmptyHeader>
                <EmptyContent>
                  <Button size='sm' onClick={() => setComposeOpen(true)}>
                    Publish the first broadcast
                  </Button>
                </EmptyContent>
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

      <NotificationBroadcastSheet open={composeOpen} onOpenChange={setComposeOpen} />
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
