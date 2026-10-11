import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation } from '@connectrpc/connect-query'
import { Plus, ShieldOff } from '@keyline-icons/react'
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
  AlertDialogTitle,
  AlertDialogTrigger
} from 'uilibs/components/base/alert-dialog'
import { Button } from 'uilibs/components/base/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger
} from 'uilibs/components/base/dialog'
import { Field, FieldDescription, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
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
import { listPageInput, usePaginatedList } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { Can } from '#/libraries/guard/can'
import { dataTableStyles } from '#/styles/element/data-table.stylex'
import { styles } from '#/styles/pages/audit.stylex'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  AddBlocklistEntryRequestSchema,
  BlocklistService,
  ListBlocklistEntriesRequestSchema,
  RemoveBlocklistEntryRequestSchema,
  type BlocklistEntry
} from '~/codegen/identity_pb'

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium' })

function formatWireInstant(value: Timestamp | undefined): string {
  if (!value) return '—'
  return instantFormat.format(timestampDate(value))
}

/**
 * The sign-up blocklist screen (phase 3): the entries no open-mode sign-up
 * may claim. An entry is one address or one `@domain` — the wildcard
 * patterns are not part of this grammar — and adding an entry the list
 * already carries is the stored row, not an error.
 */
export function BlocklistView({
  page = 1,
  onPageChange
}: {
  page?: number
  onPageChange?: (next: number) => void
}) {
  const [error, setError] = useState<string | null>(null)
  const listInput = create(ListBlocklistEntriesRequestSchema, listPageInput(page))
  const entries = usePaginatedList(BlocklistService.method.listBlocklistEntries, listInput)
  const rows = entries.data?.entries ?? []
  const summary = entries.summary

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
            Signup blocklist
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The identifiers no open-mode sign-up may claim — one address, or one domain with the
            leading @.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Blocked identifiers</CardTitle>
            <CardDescription>
              A blocked address also blocks its subaddressed variants (`name+tag@…`).
            </CardDescription>
            <CardAction>
              <AddEntryDialog onAdded={() => setError(null)} />
            </CardAction>
          </CardHeader>
          <CardContent>
            {entries.isPending ? (
              <Spinner />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>The blocklist is empty</EmptyTitle>
                  <EmptyDescription>
                    Every open-mode sign-up identifier is admitted.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Pattern</TableHead>
                    <TableHead>Added by</TableHead>
                    <TableHead>Added</TableHead>
                    <TableHead>Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((entry) => (
                    <EntryRow key={entry.id} entry={entry} />
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
            </CardFooter>
          ) : null}
        </Card>
      </div>
    </div>
  )
}

function EntryRow({ entry }: { entry: BlocklistEntry }) {
  return (
    <TableRow>
      <TableCell>{entry.pattern}</TableCell>
      <TableCell>{entry.createdBy || '—'}</TableCell>
      <TableCell style={dataTableStyles.instant}>{formatWireInstant(entry.createdAt)}</TableCell>
      <TableCell>
        <RemoveEntryDialog entry={entry} />
      </TableCell>
    </TableRow>
  )
}

const localStyles = stylex.create({
  error: {
    marginTop: 8
  }
})

function AddEntryDialog({ onAdded }: { onAdded: () => void }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [pattern, setPattern] = useState('')

  const adding = useMutation(BlocklistService.method.addBlocklistEntry, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      setPattern('')
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: BlocklistService.method.listBlocklistEntries,
          cardinality: 'finite'
        })
      })
      onAdded()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Can permission='blocklist:*:create'>
        <DialogTrigger render={<Button size='sm' />}>
          <Plus size={16} /> Add entry
        </DialogTrigger>
      </Can>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Block an identifier</DialogTitle>
          <DialogDescription>
            One email address (`john.doe@example.com`) or one domain (`@example.com`, every address
            at it). The entry is stored lowercased and matched case-insensitively.
          </DialogDescription>
        </DialogHeader>
        <Field id='blocklist-pattern'>
          <FieldLabel htmlFor='blocklist-pattern'>Identifier</FieldLabel>
          <Input
            id='blocklist-pattern'
            value={pattern}
            onChange={(event) => setPattern(event.target.value)}
          />
          <FieldDescription>
            The service refuses a pattern outside the grammar — the wildcard patterns are not part
            of it.
          </FieldDescription>
        </Field>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
            {failed}
          </Text>
        ) : null}
        <DialogFooter>
          <Button variant='ghost' onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button
            disabled={adding.isPending || pattern.trim().length === 0}
            onClick={() =>
              adding.mutate(create(AddBlocklistEntryRequestSchema, { pattern: pattern.trim() }))
            }
          >
            {adding.isPending ? <Spinner /> : <ShieldOff size={16} />}
            Add entry
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function RemoveEntryDialog({ entry }: { entry: BlocklistEntry }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)

  const removing = useMutation(BlocklistService.method.removeBlocklistEntry, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: BlocklistService.method.listBlocklistEntries,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <Can permission='blocklist:*:delete'>
        <AlertDialogTrigger render={<Button variant='outline' size='xs' />}>
          Remove
        </AlertDialogTrigger>
      </Can>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Unblock {entry.pattern}?</AlertDialogTitle>
          <AlertDialogDescription>
            The identifier becomes claimable again by every open-mode sign-up.
          </AlertDialogDescription>
          {failed ? (
            <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
              {failed}
            </Text>
          ) : null}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant='destructive'
            disabled={removing.isPending}
            onClick={() =>
              removing.mutate(create(RemoveBlocklistEntryRequestSchema, { id: entry.id }))
            }
          >
            {removing.isPending ? <Spinner /> : <ShieldOff size={16} />}
            Remove
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
