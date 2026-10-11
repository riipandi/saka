import * as stylex from '@stylexjs/stylex'
import { createFileRoute, Link, Outlet, useRouterState } from '@tanstack/react-router'
import { Tabs, TabsContent, TabsList, TabsTrigger } from 'uilibs/components/base/tabs'
import { Text } from 'uilibs/components/extra/text'
import { pageStyles } from '#/styles/pages/page.stylex'

const accountTabs = [
  {
    id: 'profile',
    label: 'Profile',
    to: '/account/profile',
    description: 'Your name, picture, and workspace preferences.'
  },
  {
    id: 'security',
    label: 'Security',
    to: '/account/security',
    description: 'The password, passkeys, and second factor this account signs in with.'
  },
  {
    id: 'sessions',
    label: 'Sessions',
    to: '/account/sessions',
    description: 'The devices this account is signed in from.'
  },
  {
    id: 'email',
    label: 'Email',
    to: '/account/email',
    description: 'The address this account answers to, its proof, and its changes.'
  },
  {
    id: 'access',
    label: 'Access',
    to: '/account/access',
    description: 'Provider identities, applications you approved, and personal access tokens.'
  },
  {
    id: 'audit',
    label: 'Activity',
    to: '/account/audit',
    description: 'What happened to this account, newest first.'
  }
] as const

export const Route = createFileRoute('/(app)/account')({
  component: SettingsShell
})

function SettingsShell() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  // Notifications lives beside the tabs: it renders its own full-page
  // header through the Outlet, with no Settings chrome around it.
  const current = accountTabs.find(
    (tab) => pathname === tab.to || pathname.startsWith(`${tab.to}/`)
  )
  if (current === undefined) return <Outlet />

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
            {current?.description ?? 'Your account.'}
          </Text>
        </div>
      </div>
      <Tabs value={current?.id ?? 'profile'}>
        <TabsList>
          {accountTabs.map((tab) => (
            <TabsTrigger
              key={tab.id}
              value={tab.id}
              nativeButton={false}
              render={
                <Link
                  to={
                    tab.id === 'profile'
                      ? '/account/profile'
                      : tab.id === 'security'
                        ? '/account/security'
                        : tab.id === 'sessions'
                          ? '/account/sessions'
                          : tab.id === 'email'
                            ? '/account/email'
                            : tab.id === 'access'
                              ? '/account/access'
                              : '/account/audit'
                  }
                />
              }
            >
              {tab.label}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value={current?.id ?? 'profile'}>
          <Outlet />
        </TabsContent>
      </Tabs>
    </div>
  )
}
