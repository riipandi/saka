import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Switch } from 'uilibs/components/base/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from 'uilibs/components/base/tabs'
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
import type { Notification } from '~/codegen/notification_pb'
import { NotificationService } from '~/codegen/notification_pb'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The account's inbox (Wave 5 phase 3): the notifications that target the
 * caller, newest first, filtered by category and by the receipts they
 * carry. Marking read is the account's own act — the receipt is written
 * per notification, or over the whole inbox at once.
 */
export function AccountNotificationsView() {
  const [tab, setTab] = useState<'all' | 'announcement' | 'system'>('all')

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
            Notifications
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            What the deployment addressed to you. The read state is your own receipt — nothing is
            marked read until you say so.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Tabs
          value={tab}
          onValueChange={(next) => {
            if (next === 'all' || next === 'announcement' || next === 'system') setTab(next)
          }}
        >
          <TabsList>
            <TabsTrigger value='all'>All</TabsTrigger>
            <TabsTrigger value='announcement'>Announcements</TabsTrigger>
            <TabsTrigger value='system'>System</TabsTrigger>
          </TabsList>
          <TabsContent value='all'>
            <InboxCard category={undefined} />
          </TabsContent>
          <TabsContent value='announcement'>
            <InboxCard category='announcement' />
          </TabsContent>
          <TabsContent value='system'>
            <InboxCard category='system' />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}

function InboxCard({ category }: { category?: string }) {
  const [unreadOnly, setUnreadOnly] = useState(false)
  const [page, setPage] = useState(1)
  const queryClient = useQueryClient()

  const inbox = useQuery(
    NotificationService.method.listNotifications,
    { ...listPageInput(page), unreadOnly: unreadOnly || undefined, category },
    { retry: false }
  )
  const markAll = useMutation(NotificationService.method.markAllNotificationsRead, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: NotificationService.method.listNotifications,
          cardinality: 'finite'
        })
      })
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: NotificationService.method.unreadCount,
          cardinality: 'finite'
        })
      })
    }
  })

  const rows = inbox.data?.notifications ?? []
  const summary = decodeListMetadata(inbox.data?.metadata)

  return (
    <Card>
      <CardHeader>
        <CardTitle>Your inbox</CardTitle>
        <CardDescription>
          {category === undefined
            ? 'Everything the deployment addressed to you, newest first.'
            : category === 'announcement'
              ? 'The broadcasts the deployment published.'
              : 'The notices about your own account.'}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div {...stylex.props(styles.toolbar)}>
          <label {...stylex.props(styles.toggle)}>
            <Switch
              id='inbox-unread-only'
              checked={unreadOnly}
              onCheckedChange={(checked) => {
                setUnreadOnly(checked)
                setPage(1)
              }}
              aria-label='Unread only'
            />
            <span>Unread only</span>
          </label>
          <Button
            variant='outline'
            size='sm'
            disabled={markAll.isPending || inbox.isPending}
            onClick={() => markAll.mutate({})}
          >
            {markAll.isPending ? <Spinner /> : 'Mark all read'}
          </Button>
        </div>

        {inbox.isPending ? (
          <Spinner />
        ) : inbox.isError ? (
          <Text variant='body-2' color='critical'>
            {getErrorMessage(inbox.error)}
          </Text>
        ) : rows.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>Nothing here</EmptyTitle>
              <EmptyDescription>
                {unreadOnly
                  ? 'Every notification addressed to you carries your receipt.'
                  : 'No notification has addressed you yet.'}
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <>
            <ItemGroup>
              {rows.map((notice) => (
                <InboxRow key={notice.id} notice={notice} />
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
  )
}

function InboxRow({ notice }: { notice: Notification }) {
  const queryClient = useQueryClient()
  const markRead = useMutation(NotificationService.method.markNotificationRead, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: NotificationService.method.listNotifications,
          cardinality: 'finite'
        })
      })
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: NotificationService.method.unreadCount,
          cardinality: 'finite'
        })
      })
    }
  })

  const unread = !notice.readAt

  return (
    <Item key={notice.id}>
      <ItemContent>
        <ItemTitle>{notice.title}</ItemTitle>
        <ItemDescription>
          {notice.topic ? `${notice.topic} · ` : ''}
          {notice.body} · {formatInstant(notice.createdAt)}
          {!unread ? ` · read ${formatInstant(notice.readAt)}` : ''}
        </ItemDescription>
      </ItemContent>
      <ItemActions>
        {unread ? (
          <>
            <Badge color='primary'>unread</Badge>
            <Button
              variant='outline'
              size='sm'
              disabled={markRead.isPending}
              aria-label={`Mark ${notice.title} read`}
              onClick={() => markRead.mutate({ id: notice.id })}
            >
              Mark read
            </Button>
          </>
        ) : (
          <Badge color='neutral'>read</Badge>
        )}
      </ItemActions>
    </Item>
  )
}

const styles = stylex.create({
  toolbar: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    marginBottom: 16
  },
  toggle: {
    display: 'flex',
    alignItems: 'center',
    gap: 10,
    cursor: 'pointer'
  },
  pager: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'flex-end',
    gap: 12,
    marginTop: 16
  }
})
