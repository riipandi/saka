import { ChevronRight, KeyRound, Settings, Bell } from '@keyline-icons/react'
import atoms from '@stylexjs/atoms'
import * as stylex from '@stylexjs/stylex'
import { Link } from '@tanstack/react-router'
import { Card, CardContent } from 'uilibs/components/extra/card'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle
} from 'uilibs/components/extra/item'
import { Text } from 'uilibs/components/extra/text'
import { usePaginatedList } from '#/hooks/use-pagination'
import { pageStyles } from '#/styles/pages/page.stylex'
import { ApiKeyService } from '~/codegen/api_key_pb'
import { AuditLogService } from '~/codegen/auditlog_pb'
import { MultifactorService, SessionService } from '~/codegen/authn_pb'
import { NotificationService } from '~/codegen/notification_pb'
import { WebAuthnService } from '~/codegen/webauthn_pb'
import { ActivityCard, Tile } from './-overview-tile'

/** A session counts while it is not revoked and its refresh window has not passed. */
function isLive(session: { revokedAt?: string; expiresAt: string }): boolean {
  return !session.revokedAt && Date.parse(session.expiresAt) > Date.now()
}

function grid() {
  return stylex.props(
    atoms.display.grid,
    pageStyles.cardsGrid,
    pageStyles.cardsGridMedium,
    pageStyles.cardsGridLarge,
    pageStyles.cardsGridXLarge
  )
}

/**
 * The overview every signed-in account sees: its own sessions, sign-in
 * methods, unread notifications, API keys, and latest activity. Every read is
 * a call the caller's own session is already allowed to make.
 */
export function AccountOverview() {
  const sessions = usePaginatedList(SessionService.method.listSessions, { page: 1, limit: 100 })
  const passkeys = usePaginatedList(WebAuthnService.method.listCredentials, {})
  const authenticators = usePaginatedList(MultifactorService.method.listTotpEnrollments, {})
  const unread = usePaginatedList(NotificationService.method.listNotifications, {
    page: 1,
    limit: 1,
    unreadOnly: true
  })
  const keys = usePaginatedList(ApiKeyService.method.listAPIKeys, { page: 1, limit: 1 })
  const activity = usePaginatedList(AuditLogService.method.list, { page: 1, limit: 5 })

  const live = sessions.data?.sessions.filter(isLive).length
  const passkeyCount = passkeys.data?.credentials.length
  const authenticatorCount = authenticators.data?.enrollments.length
  const noMethod = passkeyCount === 0 && authenticatorCount === 0

  return (
    <>
      <div {...grid()}>
        <Tile
          label='Active sessions'
          value={live}
          loading={sessions.isPending}
          failed={sessions.isError}
          hint='Browsers and devices signed in as you.'
          to='/account/sessions'
        />
        <Tile
          label='Passkeys'
          value={passkeyCount}
          loading={passkeys.isPending}
          failed={passkeys.isError}
          hint={noMethod ? 'None yet — add one in Security.' : 'Sign in with what you are.'}
          to='/account/security'
        />
        <Tile
          label='Authenticator apps'
          value={authenticatorCount}
          loading={authenticators.isPending}
          failed={authenticators.isError}
          hint={noMethod ? 'None yet — add one in Security.' : 'A second factor at sign-in.'}
          to='/account/security'
        />
        <Tile
          label='Unread notifications'
          value={unread.summary?.totalItems}
          loading={unread.isPending}
          failed={unread.isError}
          to='/account/notifications'
        />
        <Tile
          label='API keys'
          value={keys.summary?.totalItems}
          loading={keys.isPending}
          failed={keys.isError}
          hint='Keys you created for scripts and integrations.'
          to='/account/api-keys'
        />
      </div>

      <div {...stylex.props(pageStyles.quickCard)}>
        <ActivityCard
          title='Recent activity on your account'
          logs={activity.data?.logs ?? []}
          loading={activity.isPending}
          failed={activity.isError}
          emptyText='Nothing recorded yet.'
        />
      </div>

      <Card style={pageStyles.quickCard}>
        <CardContent>
          <Text render={<p />} variant='body-1' weight='semibold' style={pageStyles.quickTitle}>
            Quick links
          </Text>
          <ItemGroup>
            {(
              [
                ['/account/profile', Settings, 'Settings', 'Your names, language, and picture.'],
                [
                  '/account/security',
                  KeyRound,
                  'Security',
                  'Password, passkeys, and authenticator apps.'
                ],
                ['/account/notifications', Bell, 'Notifications', 'What the deployment told you.']
              ] as const
            ).map(([to, Icon, title, description]) => (
              <Item
                key={to}
                size='sm'
                render={
                  <Link to={to}>
                    <ItemMedia variant='icon'>
                      <Icon size={16} />
                    </ItemMedia>
                    <ItemContent>
                      <ItemTitle>{title}</ItemTitle>
                      <ItemDescription>{description}</ItemDescription>
                    </ItemContent>
                    <ItemActions>
                      <ChevronRight size={16} />
                    </ItemActions>
                  </Link>
                }
              />
            ))}
          </ItemGroup>
        </CardContent>
      </Card>
    </>
  )
}
