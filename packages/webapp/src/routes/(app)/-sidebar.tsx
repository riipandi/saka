import {
  History,
  LaptopSmartphone,
  LayoutDashboard,
  Mail,
  Power,
  Settings,
  ShieldCheck
} from '@keyline-icons/react'
import { PanelLeft, PanelRight } from '@keyline-icons/react'
// The two-tone variant paints the accent the stroke set only outlines, so
// the selected item reads filled where its idle siblings stay outlined.
import {
  History as HistoryTwoTone,
  LaptopSmartphone as LaptopSmartphoneTwoTone,
  LayoutDashboard as LayoutDashboardTwoTone,
  Mail as MailTwoTone,
  Settings as SettingsTwoTone,
  ShieldCheck as ShieldCheckTwoTone
} from '@keyline-icons/react/two-tone'
import * as stylex from '@stylexjs/stylex'
import { Link, useRouterState } from '@tanstack/react-router'
import type { LinkProps } from '@tanstack/react-router'
import { useState } from 'react'
import { ThemeSwitcher } from 'uilibs/theme'
import { useAuthentication } from '#/hooks/use-auth'
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

const secondaryItems: NavItem[] = [
  { icon: Settings, activeIcon: SettingsTwoTone, label: 'Settings', href: '/settings' },
  {
    icon: LaptopSmartphone,
    activeIcon: LaptopSmartphoneTwoTone,
    label: 'Sessions',
    href: '/account/sessions'
  },
  { icon: Mail, activeIcon: MailTwoTone, label: 'Email', href: '/account/email' },
  {
    icon: ShieldCheck,
    activeIcon: ShieldCheckTwoTone,
    label: 'Security',
    href: '/account/security'
  },
  { icon: History, activeIcon: HistoryTwoTone, label: 'Audit', href: '/account/audit' }
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

export function SideNavbar({ collapsed = false, onToggleCollapse }: SideNavbarProps) {
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const { logout } = useAuthentication()
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
        {!collapsed && <p {...stylex.props(sidebarStyles.sectionLabel)}>Main</p>}
        {renderNavItems(navItems)}
        {collapsed && <div {...stylex.props(sidebarStyles.divider)} />}
        {!collapsed && <p {...stylex.props(sidebarStyles.sectionLabel)}>Account</p>}
        {renderNavItems(secondaryItems)}
      </div>

      {/* Bottom: sign out + theme switcher */}
      <div
        {...stylex.props(
          sidebarStyles.bottomSection,
          collapsed && sidebarStyles.bottomSectionCollapsed
        )}
      >
        <div
          {...stylex.props(sidebarStyles.bottomRow, collapsed && sidebarStyles.bottomRowCollapsed)}
        >
          <button
            type='button'
            onClick={logout}
            aria-label='Sign Out'
            title={collapsed ? 'Sign Out' : undefined}
            {...stylex.props(
              sidebarStyles.navItem,
              collapsed && sidebarStyles.navItemCollapsed,
              sidebarStyles.signOutButton,
              !collapsed && sidebarStyles.signOutButtonExpanded
            )}
          >
            <Power {...stylex.props(sidebarStyles.navIcon)} />
            {!collapsed && <span {...stylex.props(sidebarStyles.navLabel)}>Sign Out</span>}
          </button>
          <div
            {...stylex.props(
              sidebarStyles.switcherBox,
              collapsed && sidebarStyles.switcherBoxCollapsed
            )}
          >
            <ThemeSwitcher />
          </div>
        </div>
      </div>
    </nav>
  )
}
