import * as stylex from '@stylexjs/stylex'
import { Link } from '@tanstack/react-router'
import type { LinkProps } from '@tanstack/react-router'
import { Avatar, AvatarFallback } from 'uilibs/components/base/avatar'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from 'uilibs/components/base/dropdown-menu'
import { colors } from 'uilibs/styles/colors.stylex'
import { fontSize, fontWeight, unit } from 'uilibs/styles/tokens.stylex'
import { ThemeSwitcher } from 'uilibs/theme'
import { useAuthentication, useAuthUser } from '#/hooks/use-auth'

export const accountLinks: Array<{ label: string; to: LinkProps['to'] }> = [
  { label: 'Settings', to: '/account/profile' },
  { label: 'Notifications', to: '/account/notifications' },
  { label: 'Approve a device', to: '/device' }
]

const styles = stylex.create({
  trigger: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 36,
    height: 36,
    padding: 0,
    borderWidth: 0,
    borderRadius: 999,
    backgroundColor: 'transparent',
    cursor: 'pointer'
  },
  identity: {
    display: 'flex',
    flexDirection: 'column',
    gap: 2,
    paddingTop: unit.x1,
    paddingBottom: unit.x1,
    maxWidth: '16rem'
  },
  name: {
    fontSize: fontSize.body2,
    fontWeight: fontWeight.semibold,
    color: colors.foregroundNeutral,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap'
  },
  email: {
    fontSize: fontSize.caption1,
    color: colors.foregroundNeutralFaded,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap'
  },
  themeRow: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: unit.x3,
    paddingTop: unit.x1,
    paddingBottom: unit.x1,
    paddingLeft: unit.x2,
    paddingRight: unit.x1
  },
  themeLabel: {
    fontSize: fontSize.body2,
    color: colors.foregroundNeutral
  }
})

function initials(name: string): string {
  const parts = name.split(/\s+/).filter(Boolean)
  const letters = parts.slice(0, 2).map((part) => part[0]?.toUpperCase() ?? '')
  return letters.join('') || '?'
}

/**
 * The signed-in account menu. Every personal route that used to live in
 * the sidebar is reached from here, together with the theme cycle and
 * sign-out, so the sidebar can stay on the product's administration.
 */
export function UserMenu() {
  const user = useAuthUser()
  const { logout } = useAuthentication()
  const name =
    user?.displayName?.trim() || user?.username?.trim() || user?.email?.split('@')[0] || 'Account'

  return (
    <DropdownMenu>
      <DropdownMenuTrigger aria-label='Account menu' {...stylex.props(styles.trigger)}>
        <Avatar size='sm'>
          <AvatarFallback>{initials(name)}</AvatarFallback>
        </Avatar>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end' sideOffset={8}>
        <DropdownMenuGroup>
          <div {...stylex.props(styles.identity)}>
            <DropdownMenuLabel style={styles.name}>{name}</DropdownMenuLabel>
            {user?.email ? <span {...stylex.props(styles.email)}>{user.email}</span> : null}
          </div>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        {accountLinks.map((item) => (
          <DropdownMenuItem key={item.to} render={<Link to={item.to} />}>
            {item.label}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <div {...stylex.props(styles.themeRow)}>
          <span {...stylex.props(styles.themeLabel)}>Theme</span>
          <ThemeSwitcher />
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant='destructive' aria-label='Sign Out' onClick={() => logout()}>
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
