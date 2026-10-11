import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Layers, Plus, Users } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
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
import {
  Card,
  CardAction,
  CardContent,
  CardFooter,
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
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import {
  LIST_MAX_LIMIT,
  listPageInput,
  usePaginatedList,
  type SortOrder
} from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { Can } from '#/libraries/guard/can'
import { styles } from '#/styles/pages/audit.stylex'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  CreateUserGroupRequestSchema,
  DeleteUserGroupRequestSchema,
  GetUserGroupRequestSchema,
  ListUserGroupsRequestSchema,
  ListUsersRequestSchema,
  SetGroupAllowedOidcClientsRequestSchema,
  SetUserGroupMembersRequestSchema,
  UpdateUserGroupRequestSchema,
  UserGroupService,
  UserService,
  type UserGroup
} from '~/codegen/identity_pb'

/** The combined choices the sort control offers; the default order sends nothing. */
const SORT_CHOICES = [
  { value: 'default', label: 'Display name, A–Z', sortBy: undefined, sortOrder: undefined },
  { value: 'name:asc', label: 'Name, A–Z', sortBy: 'name', sortOrder: 'asc' },
  { value: 'user_count:desc', label: 'Most members', sortBy: 'user_count', sortOrder: 'desc' },
  { value: 'created_at:desc', label: 'Newest first', sortBy: 'created_at', sortOrder: 'desc' }
] as const

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium' })

function formatInstant(value: string | undefined | null): string {
  if (!value) return '—'
  const instant = new Date(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The group screens (phase 3): the CRUD over UserGroupService, the
 * membership as the wire's whole replace, and the OIDC allowlist roll.
 * The client ids are typed free-form — the client list surface is a later
 * wave's, and the server's refusal names an identifier it cannot match.
 */
export function GroupsView({
  page = 1,
  search,
  sortBy,
  sortOrder,
  onSearchChange,
  onSortChange,
  onPageChange
}: {
  page?: number
  search?: string
  sortBy?: string
  sortOrder?: SortOrder
  onSearchChange?: (term: string | undefined) => void
  onSortChange?: (sortBy: string | undefined, sortOrder: SortOrder | undefined) => void
  onPageChange?: (next: number) => void
}) {
  const [error, setError] = useState<string | null>(null)
  const [term, setTerm] = useState(search ?? '')

  const sort = SORT_CHOICES.find(
    (choice) => choice.sortBy === sortBy && choice.sortOrder === sortOrder
  )
  const listInput = create(ListUserGroupsRequestSchema, {
    ...listPageInput(page),
    search: search || undefined,
    sortBy: sort?.sortBy,
    sortOrder: sort?.sortOrder
  })

  const groups = usePaginatedList(UserGroupService.method.listUserGroups, listInput)
  const rows = groups.data?.groups ?? []
  const summary = groups.summary
  const currentSort = sort?.value ?? 'default'

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
            Groups
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The organizing units accounts belong to — membership is the group's to gather.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Groups</CardTitle>
            <CardDescription>Search matches the name and the display name.</CardDescription>
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
                    aria-label='Search groups'
                    placeholder='Search…'
                    value={term}
                    onChange={(event) => setTerm(event.target.value)}
                  />
                </form>
                <Select value={currentSort} onValueChange={chooseSort} items={[...SORT_CHOICES]}>
                  <SelectTrigger aria-label='Sort groups'>
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
                <GroupFormDialog mode='create' />
              </div>
            </CardAction>
          </CardHeader>
          <CardContent>
            {groups.isPending ? (
              <Spinner />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No groups match</EmptyTitle>
                  <EmptyDescription>
                    {search
                      ? `Nothing matches “${search}”. Try another term.`
                      : 'The deployment holds no groups yet.'}
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <ItemGroup>
                {rows.map((group) => (
                  <GroupRow key={group.id} group={group} />
                ))}
              </ItemGroup>
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

function GroupRow({ group }: { group: UserGroup }) {
  return (
    <Item variant='outline'>
      <ItemContent>
        <ItemTitle>{group.displayName}</ItemTitle>
        <ItemDescription>
          {group.name} · {group.userCount} member{group.userCount === 1 ? '' : 's'} · created{' '}
          {formatInstant(group.createdAt)}
        </ItemDescription>
      </ItemContent>
      <ItemActions>
        <MembersDialog group={group} />
        <ClientsDialog group={group} />
        <GroupFormDialog mode='edit' group={group} />
        <DeleteGroupDialog group={group} />
      </ItemActions>
    </Item>
  )
}

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
  },
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
    maxHeight: '20rem',
    overflowY: 'auto'
  },
  memberRow: {
    display: 'flex',
    alignItems: 'center',
    gap: 8
  },
  error: {
    marginTop: 8
  }
})

/** The create and edit dialogs share one form — the update replaces the
 * group's two fields, both required, and the duplicate name is the
 * storage's `already_exists` word. */
function GroupFormDialog({ mode, group }: { mode: 'create' | 'edit'; group?: UserGroup }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [form, setForm] = useState({
    name: group?.name ?? '',
    displayName: group?.displayName ?? ''
  })

  const invalidate = () =>
    queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: UserGroupService.method.listUserGroups,
        cardinality: 'finite'
      })
    })

  const creating = useMutation(UserGroupService.method.createUserGroup, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      void invalidate()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })
  const updating = useMutation(UserGroupService.method.updateUserGroup, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      void invalidate()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })
  const saving = mode === 'create' ? creating : updating

  const submit = () => {
    setFailed(null)
    if (mode === 'create') {
      creating.mutate(
        create(CreateUserGroupRequestSchema, {
          name: form.name.trim(),
          displayName: form.displayName.trim()
        })
      )
    } else {
      updating.mutate(
        create(UpdateUserGroupRequestSchema, {
          id: group!.id,
          name: form.name.trim(),
          displayName: form.displayName.trim()
        })
      )
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Can permission={mode === 'create' ? 'user_group:*:create' : 'user_group:*:update'}>
        <DialogTrigger
          render={
            <Button
              variant={mode === 'create' ? undefined : 'outline'}
              size={mode === 'create' ? undefined : 'xs'}
            />
          }
        >
          {mode === 'create' ? (
            <>
              <Plus size={16} /> Create group
            </>
          ) : (
            'Edit'
          )}
        </DialogTrigger>
      </Can>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{mode === 'create' ? 'Create a group' : 'Edit this group'}</DialogTitle>
          <DialogDescription>
            The name is the unique handle; the display name is what the UI shows.
          </DialogDescription>
        </DialogHeader>
        <div {...stylex.props(localStyles.form)}>
          <Field id='group-name'>
            <FieldLabel htmlFor='group-name'>Name</FieldLabel>
            <Input
              id='group-name'
              value={form.name}
              onChange={(event) => setForm({ ...form, name: event.target.value })}
            />
            <FieldDescription>3–32 letters, digits, underscores — unique.</FieldDescription>
          </Field>
          <Field id='group-display-name'>
            <FieldLabel htmlFor='group-display-name'>Display name</FieldLabel>
            <Input
              id='group-display-name'
              value={form.displayName}
              onChange={(event) => setForm({ ...form, displayName: event.target.value })}
            />
          </Field>
          {failed ? (
            <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
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
              saving.isPending ||
              form.name.trim().length === 0 ||
              form.displayName.trim().length === 0
            }
            onClick={submit}
          >
            {saving.isPending ? <Spinner /> : <Layers size={16} />}
            {mode === 'create' ? 'Create group' : 'Save changes'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** The membership as the wire's whole replace: the dialog names the whole
 * member set, the save sends it, and an empty set empties the group. */
function MembersDialog({ group }: { group: UserGroup }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [filter, setFilter] = useState('')

  const detail = useQuery(
    UserGroupService.method.getUserGroup,
    create(GetUserGroupRequestSchema, { id: group.id }),
    { enabled: open }
  )
  const candidates = useQuery(
    UserService.method.listUsers,
    create(ListUsersRequestSchema, { page: 1, limit: LIST_MAX_LIMIT }),
    {
      enabled: open
    }
  )

  const saving = useMutation(UserGroupService.method.setUserGroupMembers, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: UserGroupService.method.listUserGroups,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const everyone = candidates.data?.users ?? []
  const shown = filter.trim()
    ? everyone.filter((user) =>
        `${user.username} ${user.email} ${user.displayName}`
          .toLowerCase()
          .includes(filter.trim().toLowerCase())
      )
    : everyone

  // The dialog seeds its picks from the group's own members once the read
  // answers; from there the set is the operator's, and the save sends it
  // whole.
  useEffect(() => {
    if (!open || !detail.data) return
    setPicked(new Set((detail.data.group?.users ?? []).map((member) => member.id)))
  }, [open, detail.data])

  const toggle = (userId: string, on: boolean) => {
    setPicked((prev) => {
      const next = new Set(prev)
      if (on) next.add(userId)
      else next.delete(userId)
      return next
    })
  }

  const submit = () => {
    setFailed(null)
    saving.mutate(
      create(SetUserGroupMembersRequestSchema, { id: group.id, userIds: Array.from(picked) })
    )
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <Can permission='user_group:*:set_members'>
        <AlertDialogTrigger render={<Button variant='outline' size='xs' />}>
          <Users size={14} /> Members
        </AlertDialogTrigger>
      </Can>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Members of {group.displayName}</AlertDialogTitle>
          <AlertDialogDescription>
            The save replaces the whole member set — the accounts checked here are the group's
            members; an empty save empties the group.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {detail.isPending ? (
          <Spinner />
        ) : (
          <>
            <Field id='members-filter'>
              <FieldLabel htmlFor='members-filter'>Filter accounts</FieldLabel>
              <Input
                id='members-filter'
                value={filter}
                onChange={(event) => setFilter(event.target.value)}
              />
            </Field>
            <div {...stylex.props(localStyles.memberList)}>
              {shown.map((user) => (
                <MemberToggle
                  key={user.id}
                  member={user}
                  checked={picked.has(user.id)}
                  onToggle={(on) => toggle(user.id, on)}
                />
              ))}
            </div>
          </>
        )}
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
            {failed}
          </Text>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction disabled={saving.isPending || detail.isPending} onClick={submit}>
            {saving.isPending ? <Spinner /> : <Users size={16} />}
            Save members
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** A member row is checked when the operator's set carries it — the seed
 * came from the group's own members, and the save sends the set whole. */
function MemberToggle({
  member,
  checked,
  onToggle
}: {
  member: { id: string; displayName: string; username: string; email: string }
  checked: boolean
  onToggle: (on: boolean) => void
}) {
  return (
    <div {...stylex.props(localStyles.memberRow)}>
      <Checkbox
        checked={checked}
        onCheckedChange={(on) => onToggle(on)}
        aria-label={`Member: ${member.displayName || member.username}`}
      />
      <Text render={<span />} variant='body-2'>
        {member.displayName || member.username}
      </Text>
      <Text render={<span />} variant='caption-1' color='neutral-faded'>
        {member.username} · {member.email}
      </Text>
    </div>
  )
}

/** The allowlist roll: the client ids typed as the protocol presents them.
 * One identifier that names no client refuses the whole replacement — the
 * server's word, shown here. */
function ClientsDialog({ group }: { group: UserGroup }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [ids, setIds] = useState('')

  const saving = useMutation(UserGroupService.method.setAllowedOidcClients, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: UserGroupService.method.listUserGroups,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const enter = (next: boolean) => {
    setOpen(next)
    if (next) setFailed(null)
  }

  const submit = () => {
    setFailed(null)
    const list = ids
      .split(/[,\s]+/)
      .map((id) => id.trim())
      .filter(Boolean)
    saving.mutate(
      create(SetGroupAllowedOidcClientsRequestSchema, { id: group.id, oidcClientIds: list })
    )
  }

  return (
    <AlertDialog open={open} onOpenChange={enter}>
      <Can permission='user_group:*:update'>
        <AlertDialogTrigger render={<Button variant='outline' size='xs' />}>
          Clients
        </AlertDialogTrigger>
      </Can>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Allowed OIDC clients of {group.displayName}</AlertDialogTitle>
          <AlertDialogDescription>
            The client identifiers the group's members may reach when a client restricts itself to
            groups. An empty save empties the roll.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Field id='client-ids'>
          <FieldLabel htmlFor='client-ids'>Client ids</FieldLabel>
          <Input
            id='client-ids'
            value={ids}
            onChange={(event) => setIds(event.target.value)}
            placeholder='client-a, client-b'
          />
          <FieldDescription>Comma or whitespace separated; the replace is whole.</FieldDescription>
        </Field>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
            {failed}
          </Text>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction disabled={saving.isPending} onClick={submit}>
            {saving.isPending ? <Spinner /> : <Layers size={16} />}
            Save clients
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function DeleteGroupDialog({ group }: { group: UserGroup }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)

  const removing = useMutation(UserGroupService.method.deleteUserGroup, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: UserGroupService.method.listUserGroups,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <Can permission='user_group:*:delete'>
        <AlertDialogTrigger render={<Button variant='outline' size='xs' />}>
          Delete
        </AlertDialogTrigger>
      </Can>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {group.displayName}?</AlertDialogTitle>
          <AlertDialogDescription>
            The membership rows die with the group; the accounts themselves are untouched.
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
            onClick={() => removing.mutate(create(DeleteUserGroupRequestSchema, { id: group.id }))}
          >
            {removing.isPending ? <Spinner /> : <Layers size={16} />}
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
