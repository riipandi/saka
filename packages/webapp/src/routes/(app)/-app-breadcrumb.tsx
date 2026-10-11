import { useRouter, useRouterState } from '@tanstack/react-router'
import { Fragment } from 'react'
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator
} from 'uilibs/components/extra/breadcrumb'

const LABELS: Record<string, string> = {
  overview: 'Overview',
  admin: 'Administration',
  account: 'Account',
  settings: 'Settings',
  users: 'Users',
  groups: 'Groups',
  roles: 'Roles',
  consents: 'Consents',
  audit: 'Audit',
  blocklist: 'Blocklist',
  'signup-tokens': 'Signup tokens',
  'oidc-clients': 'OIDC clients',
  webhooks: 'Webhooks',
  'api-keys': 'API keys',
  claims: 'Custom claims',
  'oauth-providers': 'OAuth providers',
  notifications: 'Notifications',
  security: 'Security',
  sessions: 'Sessions',
  email: 'Email',
  connections: 'Connected accounts',
  'authorized-apps': 'Authorized apps',
  access: 'Access',
  profile: 'Profile',
  new: 'New client'
}

interface Crumb {
  href: string
  label: string
  link: boolean
}

function crumbsFor(pathname: string): Crumb[] {
  const parts = pathname.split('/').filter(Boolean)
  const crumbs: Crumb[] = []
  let href = ''
  for (const part of parts) {
    href += `/${part}`
    const known = LABELS[part]
    // `/admin` and `/account` are layouts, not pages.
    const link = known !== undefined && part !== 'admin' && part !== 'account'
    crumbs.push({
      href,
      label: known ?? 'Details',
      link
    })
  }
  return crumbs
}

/** The shell trail. The last crumb is the page; earlier crumbs link when they are real routes. */
export function AppBreadcrumb() {
  const router = useRouter()
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const crumbs = crumbsFor(pathname)
  if (crumbs.length === 0) return null

  return (
    <Breadcrumb>
      <BreadcrumbList>
        {crumbs.map((crumb, index) => {
          const last = index === crumbs.length - 1
          return (
            <Fragment key={crumb.href}>
              {index > 0 ? <BreadcrumbSeparator /> : null}
              <BreadcrumbItem>
                {last || !crumb.link ? (
                  <BreadcrumbPage>{crumb.label}</BreadcrumbPage>
                ) : (
                  <BreadcrumbLink
                    href={crumb.href}
                    onClick={(event) => {
                      event.preventDefault()
                      void router.navigate({ to: crumb.href })
                    }}
                  >
                    {crumb.label}
                  </BreadcrumbLink>
                )}
              </BreadcrumbItem>
            </Fragment>
          )
        })}
      </BreadcrumbList>
    </Breadcrumb>
  )
}
