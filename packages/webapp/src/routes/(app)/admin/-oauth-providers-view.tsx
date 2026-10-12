import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Plus } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle
} from 'uilibs/components/base/alert-dialog'
import { Button } from 'uilibs/components/base/button'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle
} from 'uilibs/components/extra/empty'
import {
  Item,
  ItemActions,
  ItemContent,
  ItemDescription,
  ItemGroup,
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { ListError } from '#/components/-list-error'
import { PageHeader } from '#/components/-page-header'
import { TableSkeleton } from '#/components/-skeleton-rows'
import { Can } from '#/libraries/guard/can'
import { pageStyles } from '#/styles/pages/page.stylex'
import { DeleteOAuthConnectionRequestSchema, OAuthSSOService } from '~/codegen/authn_pb'
import type { OAuthConnection } from '~/codegen/authn_pb'
import { OAuthProviderSheet } from './-oauth-provider-sheet'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

/** The wire's Timestamp printed as the operator reads it. */
function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The OAuth providers screen (phase 3): the external sign-in providers the
 * deployment trusts. A builtin connection borrows the code's own endpoints;
 * a custom one carries a discovery document or a manual endpoint set. The
 * secrets are write-only — no read answers them back.
 */
export function OAuthProvidersView() {
  const queryClient = useQueryClient()
  const connections = useQuery(OAuthSSOService.method.listConnections, {}, { retry: false })
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<OAuthConnection | null>(null)
  const [deleting, setDeleting] = useState<OAuthConnection | null>(null)
  const [removeFailed, setRemoveFailed] = useState<string | null>(null)

  const removeConnection = useMutation(OAuthSSOService.method.deleteConnection, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: OAuthSSOService.method.listConnections,
          cardinality: 'finite'
        })
      })
      setDeleting(null)
      setRemoveFailed(null)
    },
    onError: (error) => setRemoveFailed(error.message)
  })

  const list = connections.data?.connections ?? []

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
        kicker='Administration'
        title='OAuth providers'
        description='The external providers an account may sign in with — one per provider slug, the secrets sealed and never read back.'
      />

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Registered providers</CardTitle>
            <CardDescription>
              A disabled provider refuses new sign-ins but keeps its linked accounts intact.
            </CardDescription>
            <CardAction>
              <Can permission='oauth_connection:*:create'>
                <Button
                  onClick={() => {
                    setEditing(null)
                    setDialogOpen(true)
                  }}
                >
                  <Plus />
                  Add provider
                </Button>
              </Can>
            </CardAction>
          </CardHeader>
          <CardContent>
            {connections.isPending ? (
              <TableSkeleton rows={4} />
            ) : connections.isError ? (
              <ListError
                message={connections.error.message}
                onRetry={() => void connections.refetch()}
              />
            ) : list.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No providers yet</EmptyTitle>
                  <EmptyDescription>
                    Add a provider and its button appears on the sign-in page once enabled.
                  </EmptyDescription>
                </EmptyHeader>
                <EmptyContent>
                  <Can permission='oauth_connection:*:create'>
                    <Button
                      size='sm'
                      onClick={() => {
                        setEditing(null)
                        setDialogOpen(true)
                      }}
                    >
                      <Plus size={16} /> Add the first provider
                    </Button>
                  </Can>
                </EmptyContent>
              </Empty>
            ) : (
              <ItemGroup>
                {list.map((connection) => (
                  <Item key={connection.id}>
                    <ItemContent>
                      <ItemTitle>
                        {connection.provider} · {connection.displayName}
                      </ItemTitle>
                      <ItemDescription>
                        <span {...stylex.props(styles.badges)}>
                          <Badge variant='secondary'>{connection.kind}</Badge>
                          {connection.enabled ? (
                            <Badge variant='primary'>enabled</Badge>
                          ) : (
                            <Badge variant='outline'>disabled</Badge>
                          )}
                        </span>
                        {connection.scopes === ''
                          ? 'default scopes'
                          : `scopes: ${connection.scopes}`}
                        {' · '}
                        added {formatInstant(connection.createdAt)}
                      </ItemDescription>
                    </ItemContent>
                    <ItemActions>
                      <Can permission='oauth_connection:*:update'>
                        <Button
                          variant='outline'
                          size='sm'
                          onClick={() => {
                            setEditing(connection)
                            setDialogOpen(true)
                          }}
                        >
                          Edit
                        </Button>
                      </Can>
                      <Can permission='oauth_connection:*:delete'>
                        <Button
                          variant='ghost'
                          size='sm'
                          onClick={() => {
                            setRemoveFailed(null)
                            setDeleting(connection)
                          }}
                        >
                          Delete
                        </Button>
                      </Can>
                    </ItemActions>
                  </Item>
                ))}
              </ItemGroup>
            )}
          </CardContent>
        </Card>
      </div>

      <OAuthProviderSheet
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        connection={editing}
        takenProviders={list.map((one) => one.provider)}
        onDone={() =>
          void queryClient.invalidateQueries({
            queryKey: createConnectQueryKey({
              schema: OAuthSSOService.method.listConnections,
              cardinality: 'finite'
            })
          })
        }
      />

      <AlertDialog
        open={deleting !== null}
        onOpenChange={(next) => {
          if (!next) setDeleting(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove the {deleting?.displayName ?? ''} provider?</AlertDialogTitle>
            <AlertDialogDescription>
              The provider stops answering sign-ins and every linked account rides it down. This
              cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {removeFailed !== null && (
            <Text variant='body-2' color='critical'>
              {removeFailed}
            </Text>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (deleting)
                  removeConnection.mutate(
                    create(DeleteOAuthConnectionRequestSchema, { id: deleting.id })
                  )
              }}
            >
              {removeConnection.isPending ? <Spinner /> : 'Remove'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

const styles = stylex.create({
  badges: {
    display: 'inline-flex',
    gap: 6,
    marginRight: 8
  }
})
