import { Power } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { createFileRoute } from '@tanstack/react-router'
import { Avatar, AvatarFallback } from 'uilibs/components/base/avatar'
import { Button } from 'uilibs/components/base/button'
import { Field, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { Alert, AlertDescription } from 'uilibs/components/extra/alert'
import { Badge } from 'uilibs/components/extra/badge'
import { CardDescription, CardHeader, CardTitle } from 'uilibs/components/extra/card'
import { Card, CardAction, CardContent } from 'uilibs/components/extra/card'
import { Text } from 'uilibs/components/extra/text'
import { ThemeSwitcher } from 'uilibs/theme'
import { useAuthentication, useAuthUser } from '#/libraries/guard/auth-provider'
import { pageStyles } from '#/styles/pages/page.stylex'
import { styles } from '#/styles/pages/settings.stylex'

export const Route = createFileRoute('/(app)/settings')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Settings'
  }
})

function RouteComponent() {
  const user = useAuthUser()
  const { logout } = useAuthentication()
  const fullName = [user?.firstName, user?.lastName].filter(Boolean).join(' ') || 'Guest'
  const initials =
    [user?.firstName?.[0], user?.lastName?.[0]].filter(Boolean).join('').toUpperCase() || 'G'

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
            Settings
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            Adjust your workspace preferences.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Alert>
          <AlertDescription>
            Authentication runs against the DummyJSON demo API — never reuse real credentials here.
          </AlertDescription>
        </Alert>

        <Card>
          <CardHeader>
            <CardTitle>Profile</CardTitle>
            <CardDescription>Your DummyJSON account details.</CardDescription>
            <CardAction>
              <Badge variant='secondary'>{user?.email?.split('@')[1] ?? 'guest'}</Badge>
            </CardAction>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(styles.profileRow)}>
              <Avatar size='lg'>
                <AvatarFallback>{initials}</AvatarFallback>
              </Avatar>
              <Field style={styles.grow}>
                <FieldLabel>Full name</FieldLabel>
                <Input value={fullName} readOnly />
              </Field>
              <Field style={styles.grow}>
                <FieldLabel>Username</FieldLabel>
                <Input value={user?.username ?? 'guest'} readOnly />
              </Field>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Appearance</CardTitle>
            <CardDescription>Choose how the interface looks on this device.</CardDescription>
            <CardAction>
              <ThemeSwitcher />
            </CardAction>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(styles.appearanceRow)}>
              <Text render={<p />} variant='body-1' weight='semibold'>
                Color theme
              </Text>
              <Text variant='body-2' color='neutral-faded'>
                Applies instantly and persists across reloads.
              </Text>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Danger zone</CardTitle>
            <CardDescription>Irreversible actions for the current session.</CardDescription>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(styles.dangerRow)}>
              <div {...stylex.props(styles.dangerCopy)}>
                <Text render={<p />} variant='body-1' weight='semibold'>
                  Sign out
                </Text>
                <Text variant='body-2' color='neutral-faded'>
                  Terminate the current session and return to the sign-in page.
                </Text>
              </div>
              <Button variant='destructive' onClick={() => logout()}>
                <Power size={16} />
                Sign out
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
