import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { ArrowLeft } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Switch } from 'uilibs/components/base/switch'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import { Spinner } from 'uilibs/components/extra/spinner'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from 'uilibs/components/extra/table'
import { Text } from 'uilibs/components/extra/text'
import { ShowOnceSecretDialog } from '#/components/dialogs/-show-once-secret-dialog'
import { listPageInput, decodeListMetadata } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import { WebhookService } from '~/codegen/webhook_pb'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value?: Timestamp | null): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * One endpoint's detail (Wave 5 phase 4): the view, the enable switch,
 * the test dispatch, the rotation that re-mints the signing secret, and
 * the endpoint's delivery page with its attempts' redacted response
 * metadata.
 */
export function WebhookDetailView({ webhookId }: { webhookId: string }) {
  const queryClient = useQueryClient()
  const [page, setPage] = useState(1)
  const [rotatedSecret, setRotatedSecret] = useState<string | null>(null)

  const detail = useQuery(WebhookService.method.get, { id: webhookId }, { retry: false })
  const deliveries = useQuery(
    WebhookService.method.listDeliveries,
    { webhookId, ...listPageInput(page) },
    { retry: false }
  )

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: WebhookService.method.get,
        cardinality: 'finite'
      })
    })
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: WebhookService.method.listDeliveries,
        cardinality: 'finite'
      })
    })
  }

  const rotate = useMutation(WebhookService.method.rotateSecret, {
    onSuccess: (response) => {
      setRotatedSecret(response.secret)
      invalidate()
    }
  })
  const test = useMutation(WebhookService.method.test, { onSuccess: invalidate })
  const toggle = useMutation(WebhookService.method.update, { onSuccess: invalidate })

  const webhook = detail.data?.webhook

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
          <Link to='/admin/webhooks' {...stylex.props(styles.back)}>
            <ArrowLeft size={14} /> All endpoints
          </Link>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            {webhook?.name ?? 'Webhook endpoint'}
          </Text>
          {webhook && (
            <Text variant='body-2' color='neutral-faded'>
              {webhook.method} {webhook.endpoint}
              {webhook.description ? ` · ${webhook.description}` : ''}
            </Text>
          )}
        </div>
        <div {...stylex.props(styles.headerActions)}>
          <Button
            variant='outline'
            disabled={test.isPending || !webhook}
            onClick={() => webhook && test.mutate({ id: webhook.id })}
          >
            {test.isPending ? <Spinner /> : 'Send test event'}
          </Button>
          <Button
            variant='outline'
            disabled={rotate.isPending || !webhook}
            onClick={() => webhook && rotate.mutate({ id: webhook.id })}
          >
            {rotate.isPending ? <Spinner /> : 'Rotate secret'}
          </Button>
          {webhook && (
            <label {...stylex.props(styles.toggle)}>
              <Switch
                id='webhook-enabled'
                checked={webhook.enabled}
                onCheckedChange={(checked) => toggle.mutate({ id: webhook.id, enabled: checked })}
                aria-label='Endpoint enabled'
              />
              <span>{webhook.enabled ? 'Enabled' : 'Disabled'}</span>
            </label>
          )}
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Subscriptions</CardTitle>
            <CardDescription>
              The event names this endpoint receives — an empty list or the wildcard receives every
              event.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(styles.events)}>
              {webhook && webhook.eventTypes.length > 0 ? (
                webhook.eventTypes.map((event) => (
                  <Badge key={event} color='neutral'>
                    {event}
                  </Badge>
                ))
              ) : (
                <Badge color='primary'>every event</Badge>
              )}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Deliveries</CardTitle>
            <CardDescription>
              What was sent, newest first. An attempt's record is the response metadata only — a
              response body is never stored.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {deliveries.isPending ? (
              <Spinner />
            ) : deliveries.isError ? (
              <Text variant='body-2' color='critical'>
                {getErrorMessage(deliveries.error)}
              </Text>
            ) : (deliveries.data?.deliveries ?? []).length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>Nothing delivered yet</EmptyTitle>
                  <EmptyDescription>
                    The first matching event — or the test dispatch above — starts the log.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Event</TableHead>
                      <TableHead>When</TableHead>
                      <TableHead>Attempt</TableHead>
                      <TableHead>Response</TableHead>
                      <TableHead>Duration</TableHead>
                      <TableHead>Status</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(deliveries.data?.deliveries ?? []).map((delivery) => (
                      <TableRow key={delivery.id}>
                        <TableCell>{delivery.event}</TableCell>
                        <TableCell>
                          {formatInstant(delivery.createdAt)}
                          {delivery.deliveredAt
                            ? ` · delivered ${formatInstant(delivery.deliveredAt)}`
                            : ''}
                        </TableCell>
                        <TableCell>{delivery.attemptCount}</TableCell>
                        <TableCell>
                          {delivery.latestAttempt?.responseStatus
                            ? delivery.latestAttempt.responseStatus
                            : '—'}
                          {delivery.latestAttempt?.error
                            ? ` · ${delivery.latestAttempt.error}`
                            : ''}
                        </TableCell>
                        <TableCell>
                          {delivery.latestAttempt?.durationMs
                            ? `${delivery.latestAttempt.durationMs}ms`
                            : '—'}
                        </TableCell>
                        <TableCell>
                          <Badge
                            color={
                              delivery.status === 'succeeded'
                                ? 'success'
                                : delivery.status === 'failed'
                                  ? 'critical'
                                  : 'neutral'
                            }
                          >
                            {delivery.status}
                          </Badge>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
                {(() => {
                  const summary = decodeListMetadata(deliveries.data?.metadata)
                  return summary !== null &&
                    summary.totalPages !== null &&
                    summary.totalPages > 1 ? (
                    <div {...stylex.props(styles.pager)}>
                      <Button
                        variant='outline'
                        size='sm'
                        disabled={summary.page <= 1}
                        onClick={() => setPage(summary.page - 1)}
                      >
                        Previous
                      </Button>
                      <Text variant='body-2' color='neutral-faded'>
                        Page {summary.page} of {summary.totalPages}
                      </Text>
                      <Button
                        variant='outline'
                        size='sm'
                        disabled={summary.page >= (summary.totalPages ?? 1)}
                        onClick={() => setPage(summary.page + 1)}
                      >
                        Next
                      </Button>
                    </div>
                  ) : null
                })()}
              </>
            )}
          </CardContent>
        </Card>
      </div>

      <ShowOnceSecretDialog
        open={rotatedSecret !== null}
        onOpenChange={(next) => {
          if (!next) setRotatedSecret(null)
        }}
        title='The new signing secret'
        description='Deliveries sign with it from now on. It is shown once — store it now and re-register the receiver.'
        secret={rotatedSecret ?? ''}
        secretLabel='Signing secret'
      />
    </div>
  )
}

const styles = stylex.create({
  headerActions: {
    display: 'flex',
    alignItems: 'center',
    gap: 12
  },
  back: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 4,
    textDecoration: 'none',
    color: 'inherit',
    opacity: 0.7
  },
  toggle: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    cursor: 'pointer'
  },
  events: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 6
  },
  pager: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'flex-end',
    gap: 12,
    marginTop: 16
  }
})
