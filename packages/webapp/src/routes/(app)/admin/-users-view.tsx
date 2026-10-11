import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useMutation } from '@connectrpc/connect-query'
import { Plus, Users } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
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
import { UserAvatar } from '#/components/user-avatar'
import { listPageInput, usePaginatedList, type SortOrder } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { Can } from '#/libraries/guard/can'
import { dataTableStyles } from '#/styles/element/data-table.stylex'
import { styles } from '#/styles/pages/audit.stylex'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  UserService,
  CreateUserRequestSchema,
  ListUsersRequestSchema,
  type User
} from '~/codegen/identity_pb'

/** The combined choices the sort control offers; the newest-first default sends nothing. */
const SORT_CHOICES = [
  { value: 'newest', label: 'Newest first', sortBy: undefined, sortOrder: undefined },
  { value: 'created_at:asc', label: 'Oldest first', sortBy: 'created_at', sortOrder: 'asc' },
  { value: 'username:asc', label: 'Username, A–Z', sortBy: 'username', sortOrder: 'asc' },
  { value: 'email:asc', label: 'Email, A–Z', sortBy: 'email', sortOrder: 'asc' },
  {
    value: 'display_name:asc',
    label: 'Display name, A–Z',
    sortBy: 'display_name',
    sortOrder: 'asc'
  }
] as const

const localStyles = stylex.create({
  toolbar: {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: 8
  },
  search: {
    minWidth: '16rem',
    flex: 1,
    maxWidth: '22rem'
  }
})

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium' })

function formatInstant(value: string | undefined): string {
  if (!value) return '—'
  const instant = new Date(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The user management center's list page (D4): one page of the accounts
 * through the shared pagination hook, filtered by the search term the wire
 * matches against username, email, and display name, ordered by the
 * route's sort state. The creation dialog is the administrator's own door
 * — an account made here joins no groups yet; the group screens own
 * membership.
 */
export function UsersView({
  page = 1,
  search,
  sortBy,
  sortOrder,
  onSearchChange,
  onSortChange,
  onPageChange,
  onCreated
}: {
  page?: number
  search?: string
  sortBy?: string
  sortOrder?: SortOrder
  onSearchChange?: (term: string | undefined) => void
  onSortChange?: (sortBy: string | undefined, sortOrder: SortOrder | undefined) => void
  onPageChange?: (next: number) => void
  onCreated?: (user: User) => void
}) {
  const [error, setError] = useState<string | null>(null)
  const [term, setTerm] = useState(search ?? '')

  const sort = SORT_CHOICES.find(
    (choice) => choice.sortBy === sortBy && choice.sortOrder === sortOrder
  )
  const listInput = create(ListUsersRequestSchema, {
    ...listPageInput(page),
    search: search || undefined,
    sortBy: sort?.sortBy,
    sortOrder: sort?.sortOrder
  })

  const users = usePaginatedList(UserService.method.listUsers, listInput)
  const rows = users.data?.users ?? []
  const summary = users.summary
  const currentSort = sort?.value ?? 'newest'

  const chooseSort = (value: string | null) => {
    const choice = SORT_CHOICES.find((candidate) => candidate.value === value)
    if (!choice) return
    setError(null)
    onSortChange?.(choice.sortBy, choice.sortOrder)
  }

  const submitSearch = () => {
    setError(null)
    onSearchChange?.(term.trim() || undefined)
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
            Users
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The deployment's accounts — open one to manage its credential, its ban, its grants.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Accounts</CardTitle>
            <CardDescription>
              Search matches the username, the email, and the display name.
            </CardDescription>
            <CardAction>
              <div {...stylex.props(localStyles.toolbar)}>
                <form
                  onSubmit={(event) => {
                    event.preventDefault()
                    submitSearch()
                  }}
                  {...stylex.props(localStyles.search)}
                >
                  <Input
                    aria-label='Search users'
                    placeholder='Search…'
                    value={term}
                    onChange={(event) => setTerm(event.target.value)}
                  />
                </form>
                <Select value={currentSort} onValueChange={chooseSort} items={[...SORT_CHOICES]}>
                  <SelectTrigger aria-label='Sort users'>
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
                <CreateUserDialog
                  onCreated={(created) => {
                    // The navigation belongs to the route; the view only
                    // reports the answer.
                    onCreated?.(created)
                  }}
                />
              </div>
            </CardAction>
          </CardHeader>
          <CardContent>
            {users.isPending ? (
              <Spinner />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No accounts match</EmptyTitle>
                  <EmptyDescription>
                    {search
                      ? `Nothing matches “${search}”. Try another term.`
                      : 'The deployment holds no accounts yet.'}
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>User</TableHead>
                    <TableHead>Username</TableHead>
                    <TableHead>Email</TableHead>
                    <TableHead>Joined</TableHead>
                    <TableHead>Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((row) => (
                    <UserRow key={row.id} user={row} />
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

/** The search a navigation carries when it means "the defaults" — every
 * key optional on the route, but the typed Link wants them named. */
const DEFAULT_USERS_SEARCH = {
  page: undefined,
  search: undefined,
  sortBy: undefined,
  sortOrder: undefined
} as const

function UserRow({ user }: { user: User }) {
  const name = user.displayName || user.username
  return (
    <TableRow>
      <TableCell>
        <Link
          to='/admin/users/$id'
          params={{ id: user.id }}
          search={DEFAULT_USERS_SEARCH}
          {...stylex.props(userRowStyles.link)}
        >
          <UserAvatar name={name} picture={user.picture} size='sm' />
          {name}
        </Link>
      </TableCell>
      <TableCell>{user.username}</TableCell>
      <TableCell>{user.email}</TableCell>
      <TableCell style={dataTableStyles.instant}>{formatInstant(user.createdAt)}</TableCell>
      <TableCell>
        {user.bannedAt ? <Badge variant='destructive'>Banned</Badge> : null}
        {user.disabled ? <Badge variant='secondary'>Disabled</Badge> : null}
        {!user.bannedAt && !user.disabled ? <Badge variant='secondary'>Active</Badge> : null}
      </TableCell>
    </TableRow>
  )
}

const userRowStyles = stylex.create({
  link: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 8,
    color: 'inherit',
    textDecoration: 'none',
    fontWeight: 600
  },
  form: {
    display: 'flex',
    flexDirection: 'column',
    gap: 16
  }
})

/**
 * The administrator's own door for an account that exists without an
 * invitation. The password is optional — an account created without one
 * carries no credential until a later procedure sets it. Group membership
 * is deliberately not asked here: the group screens own it.
 */
function CreateUserDialog({ onCreated }: { onCreated: (user: User) => void }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [form, setForm] = useState({
    username: '',
    email: '',
    password: '',
    firstName: '',
    lastName: '',
    displayName: ''
  })

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: UserService.method.listUsers,
        cardinality: 'finite'
      })
    })

  const creating = useMutation(UserService.method.createUser, {
    onSuccess: (response) => {
      setOpen(false)
      setFailed(null)
      setForm({
        username: '',
        email: '',
        password: '',
        firstName: '',
        lastName: '',
        displayName: ''
      })
      void invalidate()
      onCreated(response.user!)
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const submit = () => {
    setFailed(null)
    const displayName = form.displayName.trim() || `${form.firstName} ${form.lastName}`.trim()
    creating.mutate(
      create(CreateUserRequestSchema, {
        username: form.username.trim(),
        email: form.email.trim(),
        password: form.password || undefined,
        firstName: form.firstName.trim(),
        lastName: form.lastName.trim(),
        displayName
      })
    )
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Can permission='user:*:create'>
        <DialogTrigger render={<Button size='sm' />}>
          <Plus size={16} /> Create user
        </DialogTrigger>
      </Can>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create an account</DialogTitle>
          <DialogDescription>
            An account made here exists without an invitation. Leave the password empty and the
            account carries no credential until one is set.
          </DialogDescription>
        </DialogHeader>
        <div {...stylex.props(userRowStyles.form)}>
          <Field id='create-username'>
            <FieldLabel htmlFor='create-username'>Username</FieldLabel>
            <Input
              id='create-username'
              value={form.username}
              onChange={(event) => setForm({ ...form, username: event.target.value })}
            />
            <FieldDescription>3–32 letters, digits, underscores.</FieldDescription>
          </Field>
          <Field id='create-email'>
            <FieldLabel htmlFor='create-email'>Email</FieldLabel>
            <Input
              id='create-email'
              type='email'
              value={form.email}
              onChange={(event) => setForm({ ...form, email: event.target.value })}
            />
          </Field>
          <Field id='create-first-name'>
            <FieldLabel htmlFor='create-first-name'>First name</FieldLabel>
            <Input
              id='create-first-name'
              value={form.firstName}
              onChange={(event) => setForm({ ...form, firstName: event.target.value })}
            />
          </Field>
          <Field id='create-last-name'>
            <FieldLabel htmlFor='create-last-name'>Last name</FieldLabel>
            <Input
              id='create-last-name'
              value={form.lastName}
              onChange={(event) => setForm({ ...form, lastName: event.target.value })}
            />
          </Field>
          <Field id='create-display-name'>
            <FieldLabel htmlFor='create-display-name'>Display name</FieldLabel>
            <Input
              id='create-display-name'
              value={form.displayName}
              onChange={(event) => setForm({ ...form, displayName: event.target.value })}
              placeholder='Composed from the names when empty'
            />
          </Field>
          <Field id='create-password'>
            <FieldLabel htmlFor='create-password'>Password</FieldLabel>
            <Input
              id='create-password'
              type='password'
              value={form.password}
              onChange={(event) => setForm({ ...form, password: event.target.value })}
            />
            <FieldDescription>Leave empty for an account without a credential.</FieldDescription>
          </Field>
          {failed ? (
            <Text render={<p />} variant='body-2' color='critical'>
              {failed}
            </Text>
          ) : null}
        </div>
        <DialogFooter>
          <Button variant='ghost' onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button
            disabled={
              creating.isPending ||
              form.username.trim().length === 0 ||
              form.email.trim().length === 0 ||
              form.firstName.trim().length === 0 ||
              form.lastName.trim().length === 0
            }
            onClick={submit}
          >
            {creating.isPending ? <Spinner /> : <Users size={16} />}
            Create user
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
