import { create } from '@bufbuild/protobuf'
import * as stylex from '@stylexjs/stylex'
import { useState } from 'react'
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from 'uilibs/components/extra/table'
import { Text } from 'uilibs/components/extra/text'
import { TableSkeleton } from '#/components/-skeleton-rows'
import { ListPagination } from '#/components/list-pagination'
import { listPageInput, usePaginatedList, type SortOrder } from '#/hooks/use-pagination'
import { dataTableStyles } from '#/styles/element/data-table.stylex'
import { styles } from '#/styles/pages/audit.stylex'
import { pageStyles } from '#/styles/pages/page.stylex'
import { AuditLogService, ListRequestSchema } from '~/codegen/auditlog_pb'
import { AccountFrame } from './-account-frame'

/** The combined choices the sort control offers; the newest-first default sends nothing. */
const SORT_CHOICES = [
  { value: 'newest', label: 'Newest first', sortBy: undefined, sortOrder: undefined },
  { value: 'created_at:asc', label: 'Oldest first', sortBy: 'created_at', sortOrder: 'asc' },
  { value: 'event:asc', label: 'Event, A–Z', sortBy: 'event', sortOrder: 'asc' },
  { value: 'username:asc', label: 'Account, A–Z', sortBy: 'username', sortOrder: 'asc' },
  { value: 'ip_address:asc', label: 'IP address, A–Z', sortBy: 'ip_address', sortOrder: 'asc' }
] as const

/** The event vocabulary the wire carries, read as sentences; unknown events stay legible. */
function formatEvent(event: string): string {
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
 * The account's own audit trail, display-only (D7): one page of the
 * caller's records through the shared pagination hook, ordered by the
 * route's sort state, with a delegated action's administrator named beside
 * the record (`actor_username`). An impersonating session is refused by the
 * guard — the page never sees that refusal in its own browser.
 */
export function AuditView({
  page = 1,
  sortBy,
  sortOrder,
  onPageChange,
  onSortChange,
  embedded = false
}: {
  embedded?: boolean
  page?: number
  sortBy?: string
  sortOrder?: SortOrder
  onPageChange?: (next: number) => void
  onSortChange?: (sortBy: string | undefined, sortOrder: SortOrder | undefined) => void
}) {
  const [error, setError] = useState<string | null>(null)

  const sort = SORT_CHOICES.find(
    (choice) => choice.sortBy === sortBy && choice.sortOrder === sortOrder
  )
  const listInput = create(ListRequestSchema, {
    ...listPageInput(page),
    sortBy: sort?.sortBy,
    sortOrder: sort?.sortOrder
  })

  const logs = usePaginatedList(AuditLogService.method.list, listInput)

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

  return (
    <AccountFrame
      embedded={embedded}
      title='Audit trail'
      description='What happened to this account, newest first, as the record was written.'
    >
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
            {logs.isPending ? (
              <TableSkeleton rows={6} />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No activity yet</EmptyTitle>
                  <EmptyDescription>
                    The account has no recorded actions on this page.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
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
                  {rows.map((record) => (
                    <TableRow key={record.id}>
                      <TableCell style={dataTableStyles.instant}>
                        {formatInstant(record.createdAt)}
                      </TableCell>
                      <TableCell>{formatEvent(record.event)}</TableCell>
                      <TableCell>
                        {record.username ? record.username : '—'}
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
    </AccountFrame>
  )
}
