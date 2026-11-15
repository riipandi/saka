import * as stylex from '@stylexjs/stylex'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { Alert, AlertDescription, AlertTitle } from 'uilibs/components/extra/alert'
import { z } from 'zod'
import { PageHeader } from '#/components/-page-header'
import { useAuthUser } from '#/hooks/use-auth'
import { usePermissions } from '#/libraries/guard/use-permissions'
import { pageStyles } from '#/styles/pages/page.stylex'
import { OverviewSections } from './-overview-sections'

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

const currentHour = new Date().getHours()
const greeting =
  currentHour < 12 ? 'Good morning' : currentHour < 18 ? 'Good afternoon' : 'Good evening'

function RouteComponent() {
  const user = useAuthUser()
  const { isAdministrator } = usePermissions()
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

      <OverviewSections administrator={isAdministrator} />
    </div>
  )
}
