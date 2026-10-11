import { create } from '@bufbuild/protobuf'
import { useQuery } from '@connectrpc/connect-query'
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
  CardFooter,
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
import { ListPagination } from '#/components/list-pagination'
import { TimeValue } from '#/components/time-value'
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
              <AuditTable records={rows} />
            )}

            {error ? (
              <Text render={<p />} variant='body-2' color='critical'>
                {error}
              </Text>
            ) : null}
          </CardContent>
          {summary && (summary.hasPrevious || summary.hasNext) ? (
            <CardFooter>
              <ListPagination
                page={summary.page}
                totalPages={summary.totalPages}
                hasPrevious={summary.hasPrevious}
                hasNext={summary.hasNext}
                onPageChange={turnPage}
              />
            </CardFooter>
          ) : null}
        </Card>
      </div>
    </div>
  )
}

/** The facet source: `FilterOptions` derives both lists from the table, so
 * the controls offer what occurs rather than a fixed vocabulary. */
/** One page of audit records, shared by the console and the user detail. */
export function AuditTable({ records }: { records: AuditLog[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Timestamp</TableHead>
          <TableHead>Event</TableHead>
          <TableHead>Account</TableHead>
          <TableHead>IP address</TableHead>
          <TableHead>Status</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {records.map((record) => (
          <TableRow key={record.id}>
            <TableCell>
              <TimeValue value={record.createdAt} />
            </TableCell>
            <TableCell>{formatAuditEvent(record.event)}</TableCell>
            <TableCell>
              {record.username ? record.username : '—'}
              {record.triggerType === 'system' ? ' · system' : ''}
              {record.actorUsername ? ` · by ${record.actorUsername}` : ''}
            </TableCell>
            <TableCell>{record.ipAddress || '—'}</TableCell>
            <TableCell>
              {record.actionStatus === 'failed' ? (
                <Badge variant='destructive'>Failed</Badge>
              ) : (
                <Badge variant='secondary'>Success</Badge>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
