import { useQuery } from '@connectrpc/connect-query'
import atoms from '@stylexjs/atoms'
import * as stylex from '@stylexjs/stylex'
import { usePaginatedList } from '#/hooks/use-pagination'
import { pageStyles } from '#/styles/pages/page.stylex'
import { AuditLogService } from '~/codegen/auditlog_pb'
import { OAuthSSOService } from '~/codegen/authn_pb'
import { OidcClientService } from '~/codegen/federation_pb'
import { UserGroupService, UserService } from '~/codegen/identity_pb'
import { QueueService, SchedulerService } from '~/codegen/system_pb'
import { WebhookService } from '~/codegen/webhook_pb'
import { ActivityCard, Tile } from './-overview-tile'

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
 * What the administrator sees above its own account: the console's state in
 * counts, each linking to its screen. The server judges every one of these
 * reads by the administrator role, so the view is mounted for that role alone.
 */
export function AdminOverview() {
  const users = usePaginatedList(UserService.method.listUsers, { page: 1, limit: 1 })
  const groups = usePaginatedList(UserGroupService.method.listUserGroups, { page: 1, limit: 1 })
  const queues = usePaginatedList(QueueService.method.listQueues, { page: 1, limit: 100 })
  const jobs = usePaginatedList(SchedulerService.method.listJobs, { page: 1, limit: 1 })
  const webhooks = usePaginatedList(WebhookService.method.list, { page: 1, limit: 1 })
  const clients = usePaginatedList(OidcClientService.method.listClients, { page: 1, limit: 1 })
  const providers = useQuery(OAuthSSOService.method.listConnections, {})
  const activity = usePaginatedList(AuditLogService.method.listAll, { page: 1, limit: 5 })

  const dead = queues.data?.queues.reduce((sum, queue) => sum + Number(queue.dead), 0)
  const pending = queues.data?.queues.reduce((sum, queue) => sum + Number(queue.pending), 0)

  return (
    <>
      <div {...grid()}>
        <Tile
          label='Users'
          value={users.summary?.totalItems}
          loading={users.isPending}
          failed={users.isError}
          to='/admin/users'
        />
        <Tile
          label='Groups'
          value={groups.summary?.totalItems}
          loading={groups.isPending}
          failed={groups.isError}
          to='/admin/groups'
        />
        <Tile
          label='Dead tasks'
          value={dead}
          loading={queues.isPending}
          failed={queues.isError}
          hint={pending === undefined ? undefined : `${pending} waiting in the queues.`}
          to='/admin/ops/queue'
          warning
        />
        <Tile
          label='Scheduled jobs'
          value={jobs.summary?.totalItems}
          loading={jobs.isPending}
          failed={jobs.isError}
          to='/admin/ops/scheduler'
        />
        <Tile
          label='Webhooks'
          value={webhooks.summary?.totalItems}
          loading={webhooks.isPending}
          failed={webhooks.isError}
          to='/admin/webhooks'
        />
        <Tile
          label='OIDC clients'
          value={clients.summary?.totalItems}
          loading={clients.isPending}
          failed={clients.isError}
          to='/admin/oidc-clients'
        />
        <Tile
          label='OAuth providers'
          value={providers.data?.connections.length}
          loading={providers.isPending}
          failed={providers.isError}
          to='/admin/oauth-providers'
        />
      </div>

      <div {...stylex.props(pageStyles.quickCard)}>
        <ActivityCard
          title='Recent activity across the deployment'
          logs={activity.data?.logs ?? []}
          loading={activity.isPending}
          failed={activity.isError}
          emptyText='Nothing recorded yet.'
        />
      </div>
    </>
  )
}
