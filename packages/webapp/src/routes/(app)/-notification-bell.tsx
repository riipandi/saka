import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Bell } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { Popover, PopoverContent, PopoverTrigger } from 'uilibs/components/base/popover'
import { Spinner } from 'uilibs/components/extra/spinner'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { styles } from '#/styles/element/bell.stylex'
import { NotificationService } from '~/codegen/notification_pb'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value?: Timestamp | null): string {
  if (!value) return ''
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '' : instantFormat.format(instant)
}

/**
 * The shell's notification bell. The badge is `UnreadCount`. Opening the
 * popover reads the first page of the caller's unread inbox (five rows).
 * The live tail that refreshes both queries is mounted once on the app
 * shell — this control renders in the mobile header and the desktop bar,
 * so it must not open the stream itself.
 */
export function NotificationBell() {
  const [open, setOpen] = useState(false)
  const queryClient = useQueryClient()
  const unread = useQuery(NotificationService.method.unreadCount, {}, { retry: false })
  const preview = useQuery(
    NotificationService.method.listNotifications,
    { page: 1, limit: 5, unreadOnly: true },
    { enabled: open, retry: false }
  )
  // The count is the wire's int64 — a bigint at runtime. The zero is
  // spelled `BigInt(0)`, not `0n`: this package's browser-test transform
  // mis-compiles the bigint literal, and a comparison against the compiled
  // form answers false even for 2n.
  const count = unread.data?.count ?? BigInt(0)
  const hasUnread = typeof count === 'bigint' && count > BigInt(0)

  const invalidate = () => {
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

  const markRead = useMutation(NotificationService.method.markNotificationRead, {
    onSuccess: invalidate
  })
  const markAll = useMutation(NotificationService.method.markAllNotificationsRead, {
    onSuccess: invalidate
  })

  const rows = preview.data?.notifications ?? []

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        aria-label={hasUnread ? `Notifications, ${count} unread` : 'Notifications'}
        {...stylex.props(styles.bell)}
      >
        <Bell size={18} strokeWidth={1.8} />
        {hasUnread && (
          <span {...stylex.props(styles.badge)} aria-hidden>
            {count > BigInt(99) ? '99+' : count.toString()}
          </span>
        )}
      </PopoverTrigger>
      <PopoverContent align='end' sideOffset={8} style={styles.flyout}>
        <div {...stylex.props(styles.flyoutHeader)}>
          <span {...stylex.props(styles.flyoutTitle)}>Notifications</span>
          {hasUnread && (
            <button
              type='button'
              disabled={markAll.isPending}
              onClick={() => markAll.mutate({})}
              {...stylex.props(styles.textButton)}
            >
              Mark all read
            </button>
          )}
        </div>
        {preview.isPending ? (
          <div {...stylex.props(styles.flyoutStatus)}>
            <Spinner />
          </div>
        ) : preview.isError ? (
          <p {...stylex.props(styles.flyoutStatus)}>{getErrorMessage(preview.error)}</p>
        ) : rows.length === 0 ? (
          <p {...stylex.props(styles.flyoutStatus)}>No unread notifications</p>
        ) : (
          <ul {...stylex.props(styles.list)}>
            {rows.map((notice) => (
              <li key={notice.id}>
                <button
                  type='button'
                  disabled={markRead.isPending}
                  aria-label={`Mark ${notice.title} read`}
                  onClick={() => markRead.mutate({ id: notice.id })}
                  {...stylex.props(styles.row)}
                >
                  <span {...stylex.props(styles.rowTitle)}>{notice.title}</span>
                  <span {...stylex.props(styles.rowBody)}>{notice.body}</span>
                  <span {...stylex.props(styles.rowTime)}>{formatInstant(notice.createdAt)}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
        <Link
          to='/account/notifications'
          onClick={() => setOpen(false)}
          {...stylex.props(styles.viewAll)}
        >
          View all
        </Link>
      </PopoverContent>
    </Popover>
  )
}
