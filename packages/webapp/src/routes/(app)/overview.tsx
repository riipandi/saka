import { ChevronRight, Link2, Power, Settings } from '@keyline-icons/react'
import atoms from '@stylexjs/atoms'
import * as stylex from '@stylexjs/stylex'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Alert, AlertDescription, AlertTitle } from 'uilibs/components/extra/alert'
import { Badge } from 'uilibs/components/extra/badge'
import { Card, CardContent } from 'uilibs/components/extra/card'
import { Item, ItemContent, ItemDescription } from 'uilibs/components/extra/item'
import { ItemGroup, ItemActions, ItemMedia, ItemTitle } from 'uilibs/components/extra/item'
import { Text } from 'uilibs/components/extra/text'
import { z } from 'zod'
import { PageHeader } from '#/components/-page-header'
import { useAuthentication, useAuthUser } from '#/hooks/use-auth'
import { pageStyles } from '#/styles/pages/page.stylex'

// The one-shot notice the admin guard's redirect carries. Anything else on
// the URL is not this page's business.
const overviewSearchSchema = z.object({
  notice: z.enum(['admin']).optional()
})

export const Route = createFileRoute('/(app)/overview')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => overviewSearchSchema.parse(search),
  staticData: {
    pageTitle: 'Overview'
  }
})

const STATS: Array<[value: string, label: string, description: string]> = [
  ['One', 'Account', 'The identity every application here authenticates against.'],
  ['Passkey', 'First', 'Sign in with what you are, not what you remember.'],
  ['Self-hosted', 'By design', 'The deployment is yours; the data stays with it.']
]

const currentHour = new Date().getHours()
const greeting =
  currentHour < 12 ? 'Good morning' : currentHour < 18 ? 'Good afternoon' : 'Good evening'

function RouteComponent() {
  const user = useAuthUser()
  const { logout } = useAuthentication()
  const navigate = useNavigate()
  const { notice } = Route.useSearch()

  // The guard-refusal notice belongs to the redirect that brought the visit
  // here; a refresh or back-navigation never replays it.
  const [arrivedAdminNotice] = useState(notice === 'admin')
  useEffect(() => {
    if (!notice) return
    void navigate({ to: '/overview', replace: true })
  }, [notice, navigate])

  const displayName =
    user?.displayName?.trim() || user?.username?.trim() || user?.email?.split('@')[0] || 'Guest'

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
        kicker='Dashboard'
        title={`${greeting}${displayName !== 'Guest' ? `, ${displayName}` : ''}!`}
        description='Welcome back to your workspace.'
      />

      {arrivedAdminNotice && (
        <Alert style={pageStyles.quickCard}>
          <AlertTitle>Administration is for administrators</AlertTitle>
          <AlertDescription>
            The console screens stayed out of reach — your account does not carry the administrator
            role.
          </AlertDescription>
        </Alert>
      )}

      <div
        {...stylex.props(
          atoms.display.grid,
          pageStyles.cardsGrid,
          pageStyles.cardsGridMedium,
          pageStyles.cardsGridLarge,
          pageStyles.cardsGridXLarge
        )}
      >
        {STATS.map(([value, label, description]) => (
          <Card key={label}>
            <CardContent>
              <Text render={<p />} variant='featured-4' weight='bold'>
                {value}
              </Text>
              <Text render={<p />} variant='body-1' weight='semibold' style={pageStyles.statLabel}>
                {label}
              </Text>
              <Text variant='body-2' color='neutral-faded'>
                {description}
              </Text>
            </CardContent>
          </Card>
        ))}
      </div>

      <Card style={pageStyles.quickCard}>
        <CardContent>
          <Text render={<p />} variant='body-1' weight='semibold' style={pageStyles.quickTitle}>
            Quick links
          </Text>
          <ItemGroup>
            <Item
              size='sm'
              render={
                <Link to='/account/profile'>
                  <ItemMedia variant='icon'>
                    <Settings size={16} />
                  </ItemMedia>
                  <ItemContent>
                    <ItemTitle>Settings</ItemTitle>
                    <ItemDescription>Theme preference and account details.</ItemDescription>
                  </ItemContent>
                  <ItemActions>
                    <ChevronRight size={16} />
                  </ItemActions>
                </Link>
              }
            />
            <Item
              size='sm'
              render={
                <a href='https://github.com/riipandi/saka' target='_blank' rel='noreferrer'>
                  <ItemMedia variant='icon'>
                    <Link2 size={16} />
                  </ItemMedia>
                  <ItemContent>
                    <ItemTitle>Source code</ItemTitle>
                    <ItemDescription>Read the source on GitHub.</ItemDescription>
                  </ItemContent>
                  <ItemActions>
                    <Badge variant='secondary'>OSS</Badge>
                  </ItemActions>
                </a>
              }
            />
            <Item size='sm' variant='muted'>
              <ItemMedia variant='icon'>
                <Power size={16} />
              </ItemMedia>
              <ItemContent>
                <ItemTitle>Sign out</ItemTitle>
                <ItemDescription>End the current DummyJSON session.</ItemDescription>
              </ItemContent>
              <ItemActions>
                <Button variant='ghost' size='xs' onClick={() => logout()}>
                  Sign out
                </Button>
              </ItemActions>
            </Item>
          </ItemGroup>
        </CardContent>
      </Card>
    </div>
  )
}
