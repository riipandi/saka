import { create } from '@bufbuild/protobuf'
import { Plus } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { Avatar, AvatarFallback, AvatarImage } from 'uilibs/components/base/avatar'
import { Button } from 'uilibs/components/base/button'
import { Input } from 'uilibs/components/base/input'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemMedia,
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { listPageInput, usePaginatedList } from '#/hooks/use-pagination'
import { Can } from '#/libraries/guard/can'
import { styles } from '#/styles/pages/audit.stylex'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  ListOidcClientsRequestSchema,
  OidcClientService,
  type OidcClient
} from '~/codegen/federation_pb'

/** The search a navigation carries when it means "the defaults" — the typed
 * Link wants the route's keys named even when they answer nothing. */
const DEFAULT_CLIENTS_SEARCH = { page: undefined, search: undefined } as const

/**
 * The client list (phase 1): the deployment's relying parties, each a door
 * into its own detail — secrets, groups, and logo live there. Creation has
 * its own route, because its answer carries the show-once secret.
 */
export function OidcClientsView({
  page = 1,
  search = '',
  onPageChange,
  onSearchChange
}: {
  page?: number
  search?: string
  onPageChange?: (next: number) => void
  onSearchChange?: (next: string | undefined) => void
}) {
  const [query, setQuery] = useState(search)
  const listInput = create(ListOidcClientsRequestSchema, {
    ...listPageInput(page),
    search: search || undefined
  })
  const clients = usePaginatedList(OidcClientService.method.listClients, listInput)
  const rows = clients.data?.clients ?? []
  const summary = clients.summary

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
            Administration
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            OIDC clients
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The relying parties this deployment signs accounts into.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Clients</CardTitle>
            <CardDescription>
              The identifier, the redirect list, and the restriction decide each client's reach.
            </CardDescription>
            <CardAction>
              <Can permission='oidc_client:*:create'>
                <Button render={<Link to='/admin/oidc-clients/new' />}>
                  <Plus />
                  New client
                </Button>
              </Can>
            </CardAction>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(localStyles.toolbar)}>
              <Input
                value={query}
                placeholder='Search by name'
                aria-label='Search clients'
                onChange={(event) => setQuery(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') onSearchChange?.(query.trim() || undefined)
                }}
              />
            </div>
            {clients.isPending ? (
              <Spinner />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No clients defined</EmptyTitle>
                  <EmptyDescription>
                    Define the first relying party to point an application's sign-in here.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <ItemGroup>
                {rows.map((client) => (
                  <ClientRow key={client.id} client={client} />
                ))}
              </ItemGroup>
            )}

            {summary && (summary.hasPrevious || summary.hasNext) ? (
              <div {...stylex.props(styles.pager)}>
                <Text variant='body-2' color='neutral-faded' style={styles.pagerStatus}>
                  {summary.totalItems === null
                    ? `Page ${summary.page}${summary.totalPages ? ` of ${summary.totalPages}` : ''}`
                    : `${(summary.firstItemIndex ?? 0) + 1}–${summary.lastItemIndex ?? ''} of ${summary.totalItems}`}
                </Text>
                <div {...stylex.props(styles.toolbarActions)}>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={!summary.hasPrevious}
                    onClick={() => onPageChange?.(summary.page - 1)}
                  >
                    Previous
                  </Button>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={summary.hasNext === false}
                    onClick={() => onPageChange?.(summary.page + 1)}
                  >
                    Next
                  </Button>
                </div>
              </div>
            ) : null}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

function ClientRow({ client }: { client: OidcClient }) {
  return (
    <Item
      variant='outline'
      render={
        <Link
          to='/admin/oidc-clients/$id'
          params={{ id: client.id }}
          search={DEFAULT_CLIENTS_SEARCH}
        />
      }
    >
      <ItemMedia>
        <Avatar>
          {client.hasLogo && client.logoUrl ? (
            <AvatarImage src={client.logoUrl} alt={client.name} />
          ) : null}
          <AvatarFallback>{client.id.charAt(0).toUpperCase()}</AvatarFallback>
        </Avatar>
      </ItemMedia>
      <ItemContent>
        <ItemTitle>{client.name}</ItemTitle>
        <ItemDescription>
          {client.id} · {client.callbackUrls.length} callback
          {client.callbackUrls.length === 1 ? '' : 's'}
        </ItemDescription>
      </ItemContent>
      <ItemActions>
        {client.clientType === 'cimd' ? <Badge variant='secondary'>CIMD</Badge> : null}
        {client.isPublic ? <Badge variant='outline'>Public</Badge> : null}
        {client.isGroupRestricted ? <Badge variant='outline'>Restricted</Badge> : null}
      </ItemActions>
    </Item>
  )
}

const localStyles = stylex.create({
  toolbar: {
    display: 'flex',
    gap: 8,
    marginBottom: 12,
    maxWidth: '20rem'
  }
})
