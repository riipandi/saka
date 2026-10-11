import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Check, Copy, Delete, KeyRound, Plus } from '@keyline-icons/react'
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
import { Checkbox } from 'uilibs/components/base/checkbox'
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
import { LIST_MAX_LIMIT, listPageInput, usePaginatedList } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { Can } from '#/libraries/guard/can'
import { dataTableStyles } from '#/styles/element/data-table.stylex'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  CreateSignupTokenRequestSchema,
  DeleteSignupTokenRequestSchema,
  ListSignupTokensRequestSchema,
  ListUserGroupsRequestSchema,
  SignupService,
  UserGroupService,
  type SignupToken
} from '~/codegen/identity_pb'

/** The windows the issue dialog offers — the wire's own floor and ceiling
 * are one hour and thirty days. */
const TTL_CHOICES = [
  { value: 86400, label: 'One day' },
  { value: 604800, label: 'One week' },
  { value: 1209600, label: 'Two weeks' },
  { value: 2592000, label: 'Thirty days' }
] as const

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value: string | undefined | null): string {
  if (!value) return '—'
  const instant = new Date(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The signup token screens (phase 3): the issued invitations with their
 * usage counters, the issue dialog whose answer carries the raw token, and
 * the withdrawal of an invitation before its window closes. The raw value
 * exists only in the creation response — the show-once dialog is its whole
 * display surface, and no later procedure can read it back.
 */
export function SignupTokensView({
  page = 1,
  onPageChange
}: {
  page?: number
  onPageChange?: (next: number) => void
}) {
  const [error, setError] = useState<string | null>(null)
  const listInput = create(ListSignupTokensRequestSchema, listPageInput(page))
  const tokens = usePaginatedList(SignupService.method.listSignupTokens, listInput)
  const rows = tokens.data?.tokens ?? []
  const summary = tokens.summary

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
            Signup tokens
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The invitations accounts are created under — the raw value is shown once, at issue.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Issued tokens</CardTitle>
            <CardDescription>
              The counters are the operator's whole view of a token.
            </CardDescription>
            <CardAction>
              <IssueTokenDialog onIssued={() => setError(null)} />
            </CardAction>
          </CardHeader>
          <CardContent>
            {tokens.isPending ? (
              <Spinner />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No tokens issued</EmptyTitle>
                  <EmptyDescription>
                    Issue one to admit sign-ups in invite mode, or to pre-assign groups.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Issued</TableHead>
                    <TableHead>Usage</TableHead>
                    <TableHead>Expires</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((token) => (
                    <TokenRow key={token.id} token={token} />
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
                onPageChange={(next) => onPageChange?.(next)}
              />
            </CardFooter>
          ) : null}
        </Card>
      </div>
    </div>
  )
}

function TokenRow({ token }: { token: SignupToken }) {
  const spent = token.usageLimit > 0 && token.usageCount >= token.usageLimit
  return (
    <TableRow>
      <TableCell style={dataTableStyles.instant}>{formatInstant(token.createdAt)}</TableCell>
      <TableCell>
        {token.usageCount} of {token.usageLimit > 0 ? token.usageLimit : '1 (single)'}
      </TableCell>
      <TableCell style={dataTableStyles.instant}>{formatInstant(token.expiresAt)}</TableCell>
      <TableCell>
        <Badge variant={spent ? 'secondary' : 'outline'}>{spent ? 'Spent' : 'Active'}</Badge>
      </TableCell>
      <TableCell>
        <RevokeTokenDialog token={token} />
      </TableCell>
    </TableRow>
  )
}

const localStyles = stylex.create({
  form: {
    display: 'flex',
    flexDirection: 'column',
    gap: 16,
    minWidth: '20rem'
  },
  memberList: {
    display: 'flex',
    flexDirection: 'column',
    gap: 8,
    maxHeight: '14rem',
    overflowY: 'auto'
  },
  memberRow: {
    display: 'flex',
    alignItems: 'center',
    gap: 8
  },
  rawToken: {
    fontFamily: 'monospace',
    wordBreak: 'break-all'
  },
  showOnceRow: {
    display: 'flex',
    alignItems: 'center',
    gap: 8
  },
  error: {
    marginTop: 8
  }
})

function IssueTokenDialog({ onIssued }: { onIssued: () => void }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [ttl, setTtl] = useState<number>(TTL_CHOICES[0].value)
  const [usageLimit, setUsageLimit] = useState('')
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [issued, setIssued] = useState<{ rawToken: string } | null>(null)
  const [copied, setCopied] = useState(false)

  const groups = useQuery(
    UserGroupService.method.listUserGroups,
    create(ListUserGroupsRequestSchema, { page: 1, limit: LIST_MAX_LIMIT }),
    { enabled: open }
  )

  const issuing = useMutation(SignupService.method.createSignupToken, {
    onSuccess: (response) => {
      setFailed(null)
      // The dialog's next state is the show-once surface — the list row is
      // refreshed behind it.
      setIssued({ rawToken: response.rawToken })
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: SignupService.method.listSignupTokens,
          cardinality: 'finite'
        })
      })
      onIssued()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const enter = (next: boolean) => {
    setOpen(next)
    if (next) {
      setFailed(null)
      setIssued(null)
      setCopied(false)
      setUsageLimit('')
      setPicked(new Set())
      setTtl(TTL_CHOICES[0].value)
    }
  }

  const toggle = (groupId: string, on: boolean) => {
    setPicked((prev) => {
      const next = new Set(prev)
      if (on) next.add(groupId)
      else next.delete(groupId)
      return next
    })
  }

  const submit = () => {
    setFailed(null)
    const limit = usageLimit.trim() ? Number(usageLimit.trim()) : undefined
    issuing.mutate(
      create(CreateSignupTokenRequestSchema, {
        ttlSeconds: ttl,
        usageLimit: limit && Number.isInteger(limit) && limit > 0 ? limit : undefined,
        userGroupIds: Array.from(picked)
      })
    )
  }

  const copy = async () => {
    if (!issued) return
    await navigator.clipboard.writeText(issued.rawToken)
    setCopied(true)
  }

  return (
    <Dialog open={open} onOpenChange={enter}>
      <Can permission='signup_token:*:create'>
        <DialogTrigger render={<Button size='sm' />}>
          <Plus size={16} /> Issue token
        </DialogTrigger>
      </Can>
      <DialogContent>
        {issued ? (
          <>
            <DialogHeader>
              <DialogTitle>The token — shown once</DialogTitle>
              <DialogDescription>
                This value is the invitation's whole credential. It is not stored and cannot be read
                back; close this dialog and it is gone.
              </DialogDescription>
            </DialogHeader>
            <Text render={<p />} variant='body-1' weight='semibold' style={localStyles.rawToken}>
              {issued.rawToken}
            </Text>
            <div {...stylex.props(localStyles.showOnceRow)}>
              <Button variant='outline' size='sm' onClick={() => void copy()}>
                {copied ? <Check size={16} /> : <Copy size={16} />}
                {copied ? 'Copied' : 'Copy'}
              </Button>
              <Button onClick={() => enter(false)}>Done</Button>
            </div>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>Issue a signup token</DialogTitle>
              <DialogDescription>
                The token admits sign-ups for its window and joins every account it creates to the
                groups checked here. Unset usage means the single invitation.
              </DialogDescription>
            </DialogHeader>
            <div {...stylex.props(localStyles.form)}>
              <Field id='token-ttl'>
                <FieldLabel htmlFor='token-ttl'>Window</FieldLabel>
                <Select
                  value={String(ttl)}
                  onValueChange={(value) => {
                    const chosen = TTL_CHOICES.find((choice) => String(choice.value) === value)
                    if (chosen) setTtl(chosen.value)
                  }}
                  items={TTL_CHOICES.map((choice) => ({
                    value: String(choice.value),
                    label: choice.label
                  }))}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {TTL_CHOICES.map((choice) => (
                      <SelectItem key={choice.value} value={String(choice.value)}>
                        {choice.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <FieldDescription>Between one hour and thirty days on the wire.</FieldDescription>
              </Field>
              <Field id='token-usage'>
                <FieldLabel htmlFor='token-usage'>Usage limit</FieldLabel>
                <Input
                  id='token-usage'
                  type='number'
                  min={1}
                  max={1000}
                  value={usageLimit}
                  onChange={(event) => setUsageLimit(event.target.value)}
                  placeholder='Empty — the single invitation'
                />
              </Field>
              <div>
                <Text render={<p />} variant='body-2' weight='medium'>
                  Groups every created account joins
                </Text>
                {groups.isPending ? (
                  <Spinner />
                ) : (groups.data?.groups ?? []).length === 0 ? (
                  <Text variant='caption-1' color='neutral-faded'>
                    No groups to assign.
                  </Text>
                ) : (
                  <div {...stylex.props(localStyles.memberList)}>
                    {(groups.data?.groups ?? []).map((group) => (
                      <div key={group.id} {...stylex.props(localStyles.memberRow)}>
                        <Checkbox
                          checked={picked.has(group.id)}
                          onCheckedChange={(on) => toggle(group.id, on)}
                          aria-label={`Group: ${group.displayName}`}
                        />
                        <Text render={<span />} variant='body-2'>
                          {group.displayName}
                        </Text>
                      </div>
                    ))}
                  </div>
                )}
              </div>
              {failed ? (
                <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
                  {failed}
                </Text>
              ) : null}
            </div>
            <DialogFooter>
              <Button variant='ghost' onClick={() => enter(false)}>
                Cancel
              </Button>
              <Button disabled={issuing.isPending} onClick={submit}>
                {issuing.isPending ? <Spinner /> : <KeyRound size={16} />}
                Issue token
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

function RevokeTokenDialog({ token }: { token: SignupToken }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)

  const revoking = useMutation(SignupService.method.deleteSignupToken, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: SignupService.method.listSignupTokens,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <Can permission='signup_token:*:delete'>
        <AlertDialogTrigger render={<Button variant='outline' size='xs' />}>
          Revoke
        </AlertDialogTrigger>
      </Can>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Revoke this invitation?</AlertDialogTitle>
          <AlertDialogDescription>
            The token stops admitting sign-ups at once — this is for an invitation withdrawn before
            its window closes; a spent or expired token needs no deletion.
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
            disabled={revoking.isPending}
            onClick={() =>
              revoking.mutate(create(DeleteSignupTokenRequestSchema, { id: token.id }))
            }
          >
            {revoking.isPending ? <Spinner /> : <Delete size={16} />}
            Revoke
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
