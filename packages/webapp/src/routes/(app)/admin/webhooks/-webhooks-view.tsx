import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Plus } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger
} from 'uilibs/components/base/alert-dialog'
import { Button } from 'uilibs/components/base/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger
} from 'uilibs/components/base/dropdown-menu'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
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
import { ListError } from '#/components/-list-error'
import { PageHeader } from '#/components/-page-header'
import { TableSkeleton } from '#/components/-skeleton-rows'
import { ShowOnceSecretDialog } from '#/components/dialogs/-show-once-secret-dialog'
import { ListPagination } from '#/components/list-pagination'
import { listPageInput, decodeListMetadata } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import { WebhookService } from '~/codegen/webhook_pb'
import { WebhookCreateSheet } from './-webhook-sheet'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The endpoints console (Wave 5 phase 4): every outbound destination the
 * deployment registers, with the create flow that mints the signing
 * secret. The secret is this page's one show-once moment — the detail
 * view never sees it again.
 */
export function WebhooksView() {
  const [page, setPage] = useState(1)
  const [createOpen, setCreateOpen] = useState(false)
  const [mintedSecret, setMintedSecret] = useState<string | null>(null)
  const queryClient = useQueryClient()

  const list = useQuery(WebhookService.method.list, listPageInput(page), { retry: false })
  const remove = useMutation(WebhookService.method.delete, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: WebhookService.method.list,
          cardinality: 'finite'
        })
      })
    }
  })

  const rows = list.data?.webhooks ?? []
  const summary = decodeListMetadata(list.data?.metadata)

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
        title='Webhooks'
        description="The destinations the deployment's events go to. Deliveries are signed; the signing secret is shown exactly once, in the create and rotate answers."
        actions={
          <Button onClick={() => setCreateOpen(true)}>
            <Plus size={16} /> New endpoint
          </Button>
        }
      />

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Every endpoint</CardTitle>
            <CardDescription>
              The destinations, newest first — a deleted endpoint's delivery history survives with
              its identifier nulled.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {list.isPending ? (
              <TableSkeleton rows={6} />
            ) : list.isError ? (
              <ListError
                message={getErrorMessage(list.error)}
                onRetry={() => void list.refetch()}
              />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No endpoints registered</EmptyTitle>
                  <EmptyDescription>
                    The first destination receives the events its subscription names.
                  </EmptyDescription>
                </EmptyHeader>
                <EmptyContent>
                  <Button size='sm' onClick={() => setCreateOpen(true)}>
                    <Plus size={16} /> Register the first endpoint
                  </Button>
                </EmptyContent>
              </Empty>
            ) : (
              <>
                <ItemGroup>
                  {rows.map((webhook) => (
                    <Item key={webhook.id}>
                      <ItemContent>
                        <ItemTitle>
                          <Link to='/admin/webhooks/$id' params={{ id: webhook.id }}>
                            {webhook.name}
                          </Link>
                        </ItemTitle>
                        <ItemDescription>
                          {webhook.method} {webhook.endpoint} · created{' '}
                          {formatInstant(webhook.createdAt)}
                          {webhook.eventTypes.length > 0
                            ? ` · ${webhook.eventTypes.length} event${webhook.eventTypes.length === 1 ? '' : 's'}`
                            : ' · every event'}
                        </ItemDescription>
                      </ItemContent>
                      <ItemActions>
                        <Badge color={webhook.enabled ? 'success' : 'neutral'}>
                          {webhook.enabled ? 'enabled' : 'disabled'}
                        </Badge>
                        <DropdownMenu>
                          <DropdownMenuTrigger render={<Button variant='outline' size='sm' />}>
                            Actions
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align='end'>
                            <DropdownMenuItem
                              render={<Link to='/admin/webhooks/$id' params={{ id: webhook.id }} />}
                            >
                              View details
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              onClick={() => void navigator.clipboard.writeText(webhook.id)}
                            >
                              Copy ID
                            </DropdownMenuItem>
                            <DeleteButton
                              name={webhook.name}
                              pending={remove.isPending}
                              onConfirm={() => remove.mutate({ id: webhook.id })}
                            />
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </ItemActions>
                    </Item>
                  ))}
                </ItemGroup>
                {summary !== null && (summary.hasPrevious || summary.hasNext) ? (
                  <ListPagination
                    page={summary.page}
                    totalPages={summary.totalPages}
                    hasPrevious={summary.hasPrevious}
                    hasNext={summary.hasNext}
                    onPageChange={setPage}
                  />
                ) : null}
              </>
            )}
          </CardContent>
        </Card>
      </div>

      <WebhookCreateSheet
        open={createOpen}
        onOpenChange={setCreateOpen}
        onMinted={(secret) => setMintedSecret(secret)}
      />
      <ShowOnceSecretDialog
        open={mintedSecret !== null}
        onOpenChange={(next) => {
          if (!next) setMintedSecret(null)
        }}
        title='The signing secret'
        description='Deliveries sign with it. It is shown once — store it now; losing it means rotating the secret and re-registering the receiver.'
        secret={mintedSecret ?? ''}
        secretLabel='Signing secret'
      />
    </div>
  )
}

function DeleteButton({
  name,
  pending,
  onConfirm
}: {
  name: string
  pending: boolean
  onConfirm: () => void
}) {
  return (
    <AlertDialog>
      <AlertDialogTrigger
        render={<Button variant='outline' size='sm' aria-label={`Delete ${name}`} />}
      >
        Delete
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete "{name}"?</AlertDialogTitle>
          <AlertDialogDescription>
            The endpoint stops receiving events and its registrations go with it. The delivery
            history survives, named by the endpoint's identifier.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Keep</AlertDialogCancel>
          <AlertDialogAction disabled={pending} onClick={onConfirm}>
            {pending ? <Spinner /> : 'Delete endpoint'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
