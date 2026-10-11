import { Menu } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { createFileRoute, Outlet, redirect, useRouter } from '@tanstack/react-router'
import { useEffect } from 'react'
import { ToastProvider, Toaster, toast } from 'uilibs/components/base/toast'
import { useIsMobile } from 'uilibs/hooks/use-media-query'
import { useAuthUser } from '#/hooks/use-auth'
import { useNotificationStream } from '#/hooks/use-notification-stream'
import { closeSidebar, useSidebarOpen, useSidebarCollapsed } from '#/libraries/app.store'
import { toggleSidebar, toggleSidebarCollapsed } from '#/libraries/app.store'
import { ensureSessionLoaded } from '#/libraries/guard/auth-session'
import { isAuthenticated, isSigningOut } from '#/libraries/guard/auth-store'
import { styles } from '#/styles/element/root-layout.stylex'
import { AdminBanner } from './-admin-banner'
import { NotificationBell } from './-notification-bell'
import { SideNavbar } from './-sidebar'
import { UserMenu } from './-user-menu'

export const Route = createFileRoute('/(app)')({
  component: RouteComponent,
  beforeLoad: async ({ location }) => {
    await ensureSessionLoaded()
    if (!isAuthenticated()) {
      throw redirect({
        to: '/login',
        search: {
          return_to: location.href,
          unauthenticated: true
        }
      })
    }
  },
  staticData: {
    pageTitle: 'Dashboard'
  }
})

function RouteComponent() {
  const router = useRouter()
  const user = useAuthUser()
  const sidebarOpen = useSidebarOpen()
  const collapsed = useSidebarCollapsed()
  const isMobile = useIsMobile()

  // One live tail for the shell. The bell renders twice (mobile header
  // and desktop bar); mounting the stream there would open two sockets.
  useNotificationStream(true, (notification) => {
    toast(notification.title, {
      description: notification.body === '' ? undefined : notification.body
    })
  })

  // Close sidebar on route change (mobile). `onResolved` fires after every
  // navigation; closing an already-closed sidebar is a no-op state write.
  useEffect(() => router.subscribe('onResolved', closeSidebar), [router])

  // The session can end while the user is on a protected page — the
  // background verify found the pair dead, a refresh was refused, the
  // backend revoked it. The guard only runs on navigation, so this effect
  // is the eviction: the moment the profile is gone, so is the page. A
  // user-initiated sign-out clears the profile by design and performs its
  // own goodbye redirect — the sign-out flag keeps this effect out of that
  // race (its `unauthenticated` notice would clobber the `loggedOut` one).
  useEffect(() => {
    if (user || isSigningOut()) return
    void router.navigate({
      to: '/login',
      search: { return_to: router.state.location.href, unauthenticated: true }
    })
  }, [user, router])

  return (
    <ToastProvider>
      <main {...stylex.props(styles.layout)}>
        {/* Mobile header — full width on mobile, hidden on desktop */}
        <div {...stylex.props(styles.mobileHeader)}>
          <button
            type='button'
            onClick={toggleSidebar}
            {...stylex.props(styles.hamburger)}
            aria-label='Toggle navigation'
          >
            <Menu size={20} strokeWidth={1.8} />
          </button>
          <span {...stylex.props(styles.mobileHeaderTitle)}>Dashboard</span>
          <div {...stylex.props(styles.headerActions)}>
            <NotificationBell />
            <UserMenu />
          </div>
        </div>

        {/* Body row: sidebar + content */}
        <div {...stylex.props(styles.body)}>
          <div {...stylex.props(styles.sidebarWrapper, sidebarOpen && styles.sidebarOpen)}>
            <SideNavbar
              collapsed={!isMobile && collapsed}
              onToggleCollapse={!isMobile ? toggleSidebarCollapsed : undefined}
            />
          </div>

          {sidebarOpen && (
            <div {...stylex.props(styles.backdrop)} onClick={toggleSidebar} aria-hidden />
          )}

          <div {...stylex.props(styles.contentArea)}>
            <AdminBanner />
            <div {...stylex.props(styles.bellRow)}>
              <NotificationBell />
              <UserMenu />
            </div>
            <Outlet />
          </div>
        </div>
        <Toaster />
      </main>
    </ToastProvider>
  )
}
