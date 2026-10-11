import { create } from '@bufbuild/protobuf'
import { useQuery } from '@connectrpc/connect-query'
import { Activity } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Input } from 'uilibs/components/base/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue
} from 'uilibs/components/base/select'
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
import { listPageInput, usePaginatedList, type SortOrder } from '#/hooks/use-pagination'
import { styles, facetStyles } from '#/styles/pages/audit.stylex'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  AuditLogService,
  FilterOptionsRequestSchema,
  ListAllRequestSchema,
  type AuditLog
} from '~/codegen/auditlog_pb'

/** The combined choices the sort control offers; the newest-first default sends nothing. */
const SORT_CHOICES = [
  { value: 'newest', label: 'Newest first', sortBy: undefined, sortOrder: undefined },
  { value: 'created_at:asc', label: 'Oldest first', sortBy: 'created_at', sortOrder: 'asc' },
  { value: 'event:asc', label: 'Event, A–Z', sortBy: 'event', sortOrder: 'asc' },
  { value: 'username:asc', label: 'Account, A–Z', sortBy: 'username', sortOrder: 'asc' },
  { value: 'ip_address:asc', label: 'IP address, A–Z', sortBy: 'ip_address', sortOrder: 'asc' }
] as const

/** The event vocabulary the wire carries, read as sentences; unknown events stay legible. */
export function formatAuditEvent(event: string): string {
  const KNOWN: Record<string, string> = {
    sign_in: 'Signed in',
    account_created: 'Account created',
    account_updated: 'Account updated',
    account_deleted: 'Account deleted',
    email_verification_sent: 'Verification email sent',
    email_verified: 'Email verified',
    profile_picture_updated: 'Profile picture updated',
    profile_picture_reset: 'Profile picture reset'
  }
  return KNOWN[event] ?? event.replaceAll('_', ' ')
}

const instantFormat = new Intl.DateTimeFormat('en-GB', {
  dateStyle: 'medium',
  timeStyle: 'short'
})

function formatInstant(value: { seconds: bigint } | undefined): string {
  if (!value) return '—'
  const instant = new Date(Number(value.seconds) * 1000)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The console's audit trail (D6): `ListAll` over every account, with the
 * facets `FilterOptions` answers — the events the table holds, the accounts
 * that have activity — and the search the wire matches against usernames
 * and addresses. A facet the options query has not answered yet offers no
 * choices rather than a guessed list.
 */
export function AdminAuditView({
  page = 1,
  event,
  userId,
  search,
  sortBy,
  sortOrder,
  onPageChange,
  onFilterChange,
  onSortChange
}: {
  page?: number
  event?: string
  userId?: string
  search?: string
  sortBy?: string
  sortOrder?: SortOrder
  onPageChange?: (next: number) => void
  onFilterChange?: (next: { event?: string; userId?: string; search?: string }) => void
  onSortChange?: (sortBy: string | undefined, sortOrder: SortOrder | undefined) => void
}) {
  const [term, setTerm] = useState(search ?? '')
  const [error, setError] = useState<string | null>(null)

  const sort = SORT_CHOICES.find(
    (choice) => choice.sortBy === sortBy && choice.sortOrder === sortOrder
  )
  const listInput = create(ListAllRequestSchema, {
    ...listPageInput(page),
    event: event || undefined,
    userId: userId || undefined,
    search: search || undefined,
    sortBy: sort?.sortBy,
    sortOrder: sort?.sortOrder
  })

  const logs = usePaginatedList(AuditLogService.method.listAll, listInput)
  const options = useQuery(AuditLogService.method.filterOptions, create(FilterOptionsRequestSchema))

  const rows = logs.data?.logs ?? []
  const summary = logs.summary
  const currentSort = sort?.value ?? 'newest'

  const chooseSort = (value: string | null) => {
    if (value === null) return
    const choice = SORT_CHOICES.find((candidate) => candidate.value === value)
    if (!choice) return
    setError(null)
    onSortChange?.(choice.sortBy, choice.sortOrder)
  }

  const turnPage = (next: number) => {
    setError(null)
    onPageChange?.(next)
  }

  const applyTerm = () => {
    setError(null)
    onFilterChange?.({ event, userId, search: term.trim() || undefined })
  }

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
            Audit trail
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            Every recorded action across the deployment, newest first. A delegated action names the
            administrator behind it.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Activity</CardTitle>
            <CardDescription>
              Records are written once, after the outcome is known, and read-only here.
            </CardDescription>
            <CardAction>
              <Select value={currentSort} onValueChange={chooseSort} items={[...SORT_CHOICES]}>
                <SelectTrigger style={styles.sort}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SORT_CHOICES.map((choice) => (
                    <SelectItem key={choice.value} value={choice.value}>
                      {choice.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </CardAction>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(facetStyles.row)}>
              <Select
                value={event ?? 'all-events'}
                onValueChange={(value) => {
                  if (value === null) return
                  setError(null)
                  onFilterChange?.({
                    event: value === 'all-events' ? undefined : value,
                    userId,
                    search: term.trim() || undefined
                  })
                }}
                items={[{ value: 'all-events', label: 'Every event' }]}
              >
                <SelectTrigger style={facetStyles.event} aria-label='Filter by event'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='all-events'>Every event</SelectItem>
                  {(options.data?.events ?? []).map((name) => (
                    <SelectItem key={name} value={name}>
                      {formatAuditEvent(name)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>

              <Select
                value={userId ?? 'all-accounts'}
                onValueChange={(value) => {
                  if (value === null) return
                  setError(null)
                  onFilterChange?.({
                    event,
                    userId: value === 'all-accounts' ? undefined : value,
                    search: term.trim() || undefined
                  })
                }}
                items={[{ value: 'all-accounts', label: 'Every account' }]}
              >
                <SelectTrigger style={facetStyles.user} aria-label='Filter by account'>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='all-accounts'>Every account</SelectItem>
                  {(options.data?.users ?? []).map((user) => (
                    <SelectItem key={user.id} value={user.id}>
                      {user.username || user.id}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>

              <Input
                id='audit-search'
                aria-label='Search by account'
                placeholder='Search username or email…'
                value={term}
                onChange={(e) => setTerm(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    applyTerm()
                  }
                }}
                style={facetStyles.search}
              />
              <Button variant='outline' size='sm' onClick={applyTerm}>
                Search
              </Button>
            </div>

            {logs.isPending ? (
              <Spinner />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No activity found</EmptyTitle>
                  <EmptyDescription>No record matches the filters on this page.</EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <ItemGroup>
                {rows.map((record) => (
                  <AuditItem key={record.id} record={record} />
                ))}
              </ItemGroup>
            )}

            {error ? (
              <Text render={<p />} variant='body-2' color='critical'>
                {error}
              </Text>
            ) : null}

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
                    onClick={() => turnPage(summary.page - 1)}
                  >
                    Previous
                  </Button>
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={summary.hasNext === false}
                    onClick={() => turnPage(summary.page + 1)}
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

/** The facet source: `FilterOptions` derives both lists from the table, so
 * the controls offer what occurs rather than a fixed vocabulary. */
/** One audit record as the console and the user detail both render it. */
export function AuditItem({ record }: { record: AuditLog }) {
  return (
    <Item variant='outline'>
      <ItemMedia variant='icon'>
        <Activity {...stylex.props(styles.icon)} />
      </ItemMedia>
      <ItemContent>
        <ItemTitle>{formatAuditEvent(record.event)}</ItemTitle>
        <ItemDescription>
          {record.username ? record.username : '—'}
          {record.triggerType === 'system' ? ' · system' : ''}
          {record.ipAddress ? ` · from ${record.ipAddress}` : ''}
          {` · ${formatInstant(record.createdAt)}`}
          {record.actorUsername ? ` · by ${record.actorUsername} (delegated)` : ''}
        </ItemDescription>
      </ItemContent>
      <ItemActions>
        {record.actionStatus === 'failed' ? (
          <Badge variant='destructive'>Failed</Badge>
        ) : (
          <Badge variant='secondary'>Success</Badge>
        )}
      </ItemActions>
    </Item>
  )
}
