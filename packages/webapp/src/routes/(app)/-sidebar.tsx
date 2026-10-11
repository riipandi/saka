import {
  BadgeCheck,
  Blocks,
  Braces,
  History,
  Globe,
  KeyRound,
  Layers,
  LayoutDashboard,
  Megaphone,
  Plug,
  SquareAsterisk,
  Lock,
  Settings,
  ShieldOff,
  Users
} from '@keyline-icons/react'
import { PanelLeft, PanelRight } from '@keyline-icons/react'
// The two-tone variant paints the accent the stroke set only outlines, so
// the selected item reads filled where its idle siblings stay outlined.
import {
  BadgeCheck as BadgeCheckTwoTone,
  Blocks as BlocksTwoTone,
  Braces as BracesTwoTone,
  History as HistoryTwoTone,
  Globe as GlobeTwoTone,
  KeyRound as KeyRoundTwoTone,
  Layers as LayersTwoTone,
  LayoutDashboard as LayoutDashboardTwoTone,
  Megaphone as MegaphoneTwoTone,
  Plug as PlugTwoTone,
  SquareAsterisk as SquareAsteriskTwoTone,
  Lock as LockTwoTone,
  Settings as SettingsTwoTone,
  ShieldOff as ShieldOffTwoTone,
  Users as UsersTwoTone
} from '@keyline-icons/react/two-tone'
import * as stylex from '@stylexjs/stylex'
import { Link, useRouterState } from '@tanstack/react-router'
import type { LinkProps } from '@tanstack/react-router'
import { Fragment, useState } from 'react'
import { usePermissions } from '#/libraries/guard/use-permissions'
import { sidebarStyles } from '#/styles/element/sidebar.stylex'

interface NavItem {
  icon: React.ComponentType<{ className?: string; size?: number; strokeWidth?: number }>
  activeIcon?: React.ComponentType<{ className?: string; size?: number; strokeWidth?: number }>
  href: LinkProps['to'] | undefined
  label: string
}

const navItems: NavItem[] = [
  {
    icon: LayoutDashboard,
    activeIcon: LayoutDashboardTwoTone,
    label: 'Overview',
    href: '/overview'
  }
]

interface AdminNavItem extends NavItem {
  permission?: string
  adminOnly?: boolean
}

/**
 * Administration, split by the job the page does. Each item carries the
 * catalog slug its page consumes (`internal/authz/catalog.go`) and hides
 * individually — a group label renders only when at least one item
 * survives its gate. `adminOnly` marks the surfaces the guard serves by
 * role (the guard's `Rule: Admin`) rather than by a catalog slug: the
 * webhook and platform API-key surfaces have no catalog resource, so a
 * slug gate would hide them from the only callers the backend admits.
 * Account routes are not here; the header user menu owns them.
 */
const adminGroups: Array<{ label: string; items: AdminNavItem[] }> = [
  {
    label: 'Identity & Access',
    items: [
      {
        icon: Users,
        activeIcon: UsersTwoTone,
        label: 'Users',
        href: '/admin/users',
        permission: 'user:*:list'
      },
      {
        icon: Layers,
        activeIcon: LayersTwoTone,
        label: 'Groups',
        href: '/admin/groups',
        permission: 'user_group:*:list'
      },
      {
        icon: BadgeCheck,
        activeIcon: BadgeCheckTwoTone,
        label: 'Roles',
        href: '/admin/roles',
        permission: 'role:*:list'
      },
      {
        icon: Lock,
        activeIcon: LockTwoTone,
        label: 'Consents',
        href: '/admin/consents',
        permission: 'oidc_consent:*:list'
      }
    ]
  },
  {
    label: 'Security & Governance',
    items: [
      {
        icon: History,
        activeIcon: HistoryTwoTone,
        label: 'Audit trail',
        href: '/admin/audit',
        permission: 'audit_log:*:list'
      },
      {
        icon: ShieldOff,
        activeIcon: ShieldOffTwoTone,
        label: 'Blocklist',
        href: '/admin/blocklist',
        permission: 'blocklist:*:list'
      },
      {
        icon: KeyRound,
        activeIcon: KeyRoundTwoTone,
        label: 'Signup tokens',
        href: '/admin/signup-tokens',
        permission: 'signup_token:*:list'
      }
    ]
  },
  {
    label: 'Developer & Integrations',
    items: [
      {
        icon: Blocks,
        activeIcon: BlocksTwoTone,
        label: 'OIDC clients',
        href: '/admin/oidc-clients',
        permission: 'oidc_client:*:list'
      },
      {
        icon: Plug,
        activeIcon: PlugTwoTone,
        label: 'Webhooks',
        href: '/admin/webhooks',
        adminOnly: true
      },
      {
        icon: SquareAsterisk,
        activeIcon: SquareAsteriskTwoTone,
        label: 'API keys',
        href: '/admin/api-keys',
        adminOnly: true
      },
      {
        icon: Braces,
        activeIcon: BracesTwoTone,
        label: 'Custom claims',
        href: '/admin/claims',
        permission: 'custom_claim:*:list'
      },
      {
        icon: Globe,
        activeIcon: GlobeTwoTone,
        label: 'OAuth providers',
        href: '/admin/oauth-providers',
        permission: 'oauth_connection:*:list'
      }
    ]
  },
  {
    label: 'Platform',
    items: [
      {
        icon: Settings,
        activeIcon: SettingsTwoTone,
        label: 'Settings',
        href: '/admin/settings',
        permission: 'setting:*:read'
      },
      {
        icon: Megaphone,
        activeIcon: MegaphoneTwoTone,
        label: 'Notifications',
        href: '/admin/notifications',
        permission: 'notification:*:list'
      }
    ]
  }
]

function LogoMark() {
  return (
    <svg
      {...stylex.props(sidebarStyles.logoSvg)}
      xmlns='http://www.w3.org/2000/svg'
      viewBox='0 0 20 20'
      fill='currentColor'
    >
      <path d='M11 17a1 1 0 001.447.894l4-2A1 1 0 0017 15V9.236a1 1 0 00-1.447-.894l-4 2a1 1 0 00-.553.894V17zM15.211 6.276a1 1 0 000-1.788l-4.764-2.382a1 1 0 00-.894 0L4.789 4.488a1 1 0 000 1.788l4.764 2.382a1 1 0 00.894 0l4.764-2.382zM4.447 8.342A1 1 0 003 9.236V15a1 1 0 00.553.894l4 2A1 1 0 009 17v-5.764a1 1 0 00-.553-.894l-4-2z' />
    </svg>
  )
}

interface SideNavbarProps {
  collapsed?: boolean
  /** Toggle desktop collapse/expand. When omitted the trigger is hidden. */
  onToggleCollapse?: () => void
}

function admits(
  item: AdminNavItem,
  can: (permission: string) => boolean,
  isAdministrator: boolean
) {
  return (
    (item.adminOnly === true && isAdministrator) ||
    (item.permission !== undefined && can(item.permission))
  )
}

export function SideNavbar({ collapsed = false, onToggleCollapse }: SideNavbarProps) {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const { can, isAdministrator } = usePermissions()
  const [headerHover, setHeaderHover] = useState(false)

  const isActive = (href: LinkProps['to']) =>
    href !== undefined && (pathname === href || pathname.startsWith(href))

  const renderNavItems = (items: NavItem[]) =>
    items.map((item) => {
      const itemActive = isActive(item.href)
      const Icon = itemActive && item.activeIcon ? item.activeIcon : item.icon
      return (
        <Link
          key={item.label}
          to={item.href}
          aria-label={item.label}
          {...stylex.props(
            sidebarStyles.navItem,
            collapsed && sidebarStyles.navItemCollapsed,
            itemActive && sidebarStyles.navItemActive
          )}
        >
          <Icon {...stylex.props(sidebarStyles.navIcon)} />
          {!collapsed && <span {...stylex.props(sidebarStyles.navLabel)}>{item.label}</span>}
        </Link>
      )
    })

  return (
    <nav
      aria-label='Sidebar'
      {...stylex.props(
        sidebarStyles.container,
        collapsed ? sidebarStyles.containerCollapsed : sidebarStyles.containerExpanded
      )}
    >
      {/* Header zone: logo + hover-reveal collapse trigger */}
      <div
        onMouseEnter={() => setHeaderHover(true)}
        onMouseLeave={() => setHeaderHover(false)}
        {...stylex.props(sidebarStyles.headerZone, collapsed && sidebarStyles.headerZoneCollapsed)}
      >
        <div
          {...stylex.props(
            sidebarStyles.logoSection,
            collapsed && sidebarStyles.logoSectionCollapsed
          )}
        >
          <Link
            to='/'
            aria-label='Home'
            {...stylex.props(
              sidebarStyles.logoLink,
              collapsed && sidebarStyles.logoLinkCollapsed,
              collapsed && headerHover && sidebarStyles.logoFade
            )}
          >
            <div {...stylex.props(sidebarStyles.logoIconWrap)}>
              <LogoMark />
            </div>
            {!collapsed && (
              <div>
                <div {...stylex.props(sidebarStyles.logoText)}>Saka</div>
                <div {...stylex.props(sidebarStyles.logoVersion)}>
                  {import.meta.env.PUBLIC_APP_VERSION}
                </div>
              </div>
            )}
          </Link>
        </div>

        {onToggleCollapse && (
          <button
            type='button'
            onClick={onToggleCollapse}
            aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
            {...stylex.props(
              sidebarStyles.collapseTrigger,
              (!collapsed || headerHover) && sidebarStyles.collapseTriggerVisible,
              collapsed && sidebarStyles.collapseTriggerCollapsed
            )}
          >
            {collapsed ? <PanelRight size={16} /> : <PanelLeft size={16} />}
          </button>
        )}
      </div>

      {/* Main nav */}
      <div
        {...stylex.props(sidebarStyles.navContent, collapsed && sidebarStyles.navContentCollapsed)}
      >
        {renderNavItems(navItems)}
        {adminGroups.map((group) => {
          const items = group.items.filter((item) => admits(item, can, isAdministrator))
          if (items.length === 0) return null
          return (
            <Fragment key={group.label}>
              {collapsed && <div {...stylex.props(sidebarStyles.divider)} />}
              {!collapsed && <p {...stylex.props(sidebarStyles.sectionLabel)}>{group.label}</p>}
              {renderNavItems(items)}
            </Fragment>
          )
        })}
      </div>
    </nav>
  )
}
