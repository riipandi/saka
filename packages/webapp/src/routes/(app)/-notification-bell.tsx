import { useQuery } from '@connectrpc/connect-query'
import { Bell } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { Link } from '@tanstack/react-router'
import { toast } from 'uilibs/components/base/toast'
import { useNotificationStream } from '#/hooks/use-notification-stream'
import { styles } from '#/styles/element/bell.stylex'
import { NotificationService } from '~/codegen/notification_pb'

/**
 * The shell's notification bell (Wave 5 phase 3): the unread count the
 * badge shows is the `UnreadCount` query, kept live by the one
 * `useNotificationStream` the shell mounts — a stream event invalidates
 * the query, the badge refetches, and the same event's toast rides the
 * hook's callback. Clicking navigates to the inbox.
 */
export function NotificationBell() {
  const unread = useQuery(NotificationService.method.unreadCount, {}, { retry: false })
  // The count is the wire's int64 — a bigint at runtime. The zero is
  // spelled `BigInt(0)`, not `0n`: this package's browser-test transform
  // mis-compiles the bigint literal, and a comparison against the compiled
  // form answers false even for 2n.
  const count = unread.data?.count ?? BigInt(0)
  const hasUnread = typeof count === 'bigint' && count > BigInt(0)

  useNotificationStream(true, (notification) => {
    toast(notification.title, {
      description: notification.body === '' ? undefined : notification.body
    })
  })

  return (
    <Link
      to='/account/notifications'
      aria-label={hasUnread ? `Notifications, ${count} unread` : 'Notifications'}
      {...stylex.props(styles.bell)}
    >
      <Bell size={18} strokeWidth={1.8} />
      {hasUnread && (
        <span {...stylex.props(styles.badge)} aria-hidden>
          {count > BigInt(99) ? '99+' : count.toString()}
        </span>
      )}
    </Link>
  )
}
