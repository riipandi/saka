import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Lock, Plus, ShieldCheck } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useState } from 'react'
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
import { Checkbox } from 'uilibs/components/base/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from 'uilibs/components/base/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger
} from 'uilibs/components/base/dropdown-menu'
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
  ItemMedia,
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { ListError } from '#/components/-list-error'
import { PageHeader } from '#/components/-page-header'
import { FormSkeleton } from '#/components/-skeleton-form'
import { TableSkeleton } from '#/components/-skeleton-rows'
import { ListPagination } from '#/components/list-pagination'
import { ListSearch } from '#/components/list-search'
import { listPageInput, usePaginatedList, type SortOrder } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  AuthorizationService,
  CreateRoleRequestSchema,
  DeleteRoleRequestSchema,
  GetRoleRequestSchema,
  ListPermissionsRequestSchema,
  ListRolesRequestSchema,
  SetRolePermissionsRequestSchema,
  UpdateRoleRequestSchema,
  RoleType,
  type Role
} from '~/codegen/authz_pb'

const SORT_CHOICES = [
  { value: 'newest', label: 'Newest first', sortBy: undefined, sortOrder: undefined },
  { value: 'created_at:asc', label: 'Oldest first', sortBy: 'created_at', sortOrder: 'asc' },
  { value: 'name:asc', label: 'Name, A–Z', sortBy: 'name', sortOrder: 'asc' },
  { value: 'slug:asc', label: 'Slug, A–Z', sortBy: 'slug', sortOrder: 'asc' }
] as const

/** The slug alphabet the wire's pattern enforces. */
const SLUG_PATTERN = /^[a-z0-9_-]+$/

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium' })

function formatInstant(value: string | undefined | null): string {
  if (!value) return '—'
  const instant = new Date(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/**
 * The roles screens (phase 6, D7): the role CRUD with its permission set as
 * a checkbox tree over `ListPermissions`, and every write's copy carrying
 * the custody rule — grants move at the account's next token, not in place.
 * A system role's set is the seed's, so its tree opens read-only.
 */
export function RolesView({
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
  onSearchChange?: (term: string) => void
  onSortChange?: (sortBy: string | undefined, sortOrder: SortOrder | undefined) => void
  onPageChange?: (next: number) => void
}) {
  const [term, setTerm] = useState(search ?? '')
  const [error, setError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [permissionsFor, setPermissionsFor] = useState<Pick<Role, 'id' | 'name' | 'type'> | null>(
    null
  )
  const [editing, setEditing] = useState<Role | null>(null)
  const [deleting, setDeleting] = useState<Role | null>(null)

  const sort = SORT_CHOICES.find(
    (choice) => choice.sortBy === sortBy && choice.sortOrder === sortOrder
  )
  const listInput = create(ListRolesRequestSchema, {
    ...listPageInput(page),
    search: search || undefined,
    sortBy: sort?.sortBy,
    sortOrder: sort?.sortOrder
  })

  const roles = usePaginatedList(AuthorizationService.method.listRoles, listInput)

  const rows = roles.data?.roles ?? []
  const summary = roles.summary
  const currentSort = sort?.value ?? 'newest'

  const chooseSort = (value: string | null) => {
    if (value === null) return
    const choice = SORT_CHOICES.find((candidate) => candidate.value === value)
    if (!choice) return
    setError(null)
    onSortChange?.(choice.sortBy, choice.sortOrder)
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
      <PageHeader
        kicker='Administration'
        title='Roles'
        description="A role is a named set of permissions accounts carry. A system role's set is the seed's; a custom one is yours to shape."
        actions={
          <Button variant='primary' onClick={() => setCreating(true)}>
            <Plus size={16} /> Create role
          </Button>
        }
      />

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Defined roles</CardTitle>
            <CardDescription>The catalog the claims name, newest first.</CardDescription>
            <CardAction>
              <Select value={currentSort} onValueChange={chooseSort} items={[...SORT_CHOICES]}>
                <SelectTrigger style={localStyles.sort}>
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
            <div {...stylex.props(localStyles.searchRow)}>
              <ListSearch
                label='Search roles'
                placeholder='Search name or slug…'
                value={term}
                onValueChange={setTerm}
                onSubmit={(next) => {
                  setTerm(next)
                  onSearchChange?.(next.trim())
                }}
                style={localStyles.search}
              />
            </div>

            {roles.isPending ? (
              <TableSkeleton rows={6} />
            ) : roles.isError ? (
              <ListError
                message={getErrorMessage(roles.error)}
                onRetry={() => void roles.refetch()}
              />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No roles found</EmptyTitle>
                  <EmptyDescription>No role matches the search on this page.</EmptyDescription>
                </EmptyHeader>
                <EmptyContent>
                  <Button size='sm' onClick={() => setCreating(true)}>
                    <Plus size={16} /> Create the first role
                  </Button>
                </EmptyContent>
              </Empty>
            ) : (
              <ItemGroup>
                {rows.map((role) => (
                  <Item variant='outline' key={role.id}>
                    <ItemMedia variant='icon'>
                      {role.type === RoleType.SYSTEM ? (
                        <Lock {...stylex.props(localStyles.icon)} />
                      ) : (
                        <ShieldCheck {...stylex.props(localStyles.icon)} />
                      )}
                    </ItemMedia>
                    <ItemContent>
                      <ItemTitle>
                        {role.name}
                        {role.type === RoleType.SYSTEM ? (
                          <Badge variant='secondary' style={localStyles.typeBadge}>
                            System
                          </Badge>
                        ) : null}
                      </ItemTitle>
                      <ItemDescription>
                        {role.slug} · {role.permissionCount} permission
                        {role.permissionCount === 1 ? '' : 's'} · defined{' '}
                        {formatInstant(role.createdAt)}
                        {role.updatedAt ? ` · updated ${formatInstant(role.updatedAt)}` : ''}
                        {role.description ? ` · ${role.description}` : ''}
                      </ItemDescription>
                    </ItemContent>
                    <ItemActions>
                      <DropdownMenu>
                        <DropdownMenuTrigger render={<Button variant='outline' size='sm' />}>
                          Actions
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align='end'>
                          <DropdownMenuItem onClick={() => setPermissionsFor(role)}>
                            Permissions
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => void navigator.clipboard.writeText(role.id)}
                          >
                            Copy ID
                          </DropdownMenuItem>
                          {role.type === RoleType.CUSTOM ? (
                            <DropdownMenuItem onClick={() => setEditing(role)}>
                              Edit
                            </DropdownMenuItem>
                          ) : null}
                          {role.type === RoleType.CUSTOM ? (
                            <DropdownMenuItem
                              variant='destructive'
                              onClick={() => setDeleting(role)}
                            >
                              Delete
                            </DropdownMenuItem>
                          ) : null}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </ItemActions>
                  </Item>
                ))}
              </ItemGroup>
            )}

            {error ? (
              <Text render={<p />} variant='body-2' color='critical'>
                {error}
              </Text>
            ) : null}

            {summary && (summary.hasPrevious || summary.hasNext) ? (
              <ListPagination
                page={summary.page}
                totalPages={summary.totalPages}
                hasPrevious={summary.hasPrevious}
                hasNext={summary.hasNext}
                onPageChange={(next) => onPageChange?.(next)}
              />
            ) : null}
          </CardContent>
        </Card>
      </div>

      {creating ? (
        <CreateRoleDialog
          onClose={() => setCreating(false)}
          onCreated={(role) => {
            setCreating(false)
            setPermissionsFor(role)
          }}
        />
      ) : null}
      {permissionsFor ? (
        <PermissionsDialog role={permissionsFor} onClose={() => setPermissionsFor(null)} />
      ) : null}
      {editing ? <EditRoleDialog role={editing} onClose={() => setEditing(null)} /> : null}
      {deleting ? <DeleteRoleDialog role={deleting} onClose={() => setDeleting(null)} /> : null}
    </div>
  )
}

function CreateRoleDialog({
  onClose,
  onCreated
}: {
  onClose: () => void
  onCreated: (role: Pick<Role, 'id' | 'name' | 'type'>) => void
}) {
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [description, setDescription] = useState('')
  const [failed, setFailed] = useState<string | null>(null)

  const creating = useMutation(AuthorizationService.method.createRole, {
    onSuccess: (response) => {
      setFailed(null)
      if (response.role) onCreated(response.role)
      onClose()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const valid =
    name.trim().length > 0 &&
    SLUG_PATTERN.test(slug.trim()) &&
    slug.trim().length >= 1 &&
    slug.trim().length <= 64

  const submit = () => {
    setFailed(null)
    if (!valid) {
      setFailed(
        'Give the role a name and a slug of lowercase letters, digits, dashes, or underscores.'
      )
      return
    }
    creating.mutate(
      create(CreateRoleRequestSchema, {
        name: name.trim(),
        slug: slug.trim(),
        description: description.trim()
      })
    )
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create a role</DialogTitle>
          <DialogDescription>
            The role is born with no permissions; the tree opens after creation.
          </DialogDescription>
        </DialogHeader>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical' id='create-role-error'>
            {failed}
          </Text>
        ) : null}
        <Field id='role-name'>
          <FieldLabel htmlFor='role-name'>Name</FieldLabel>
          <Input
            id='role-name'
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder='Reviewers'
          />
        </Field>
        <Field id='role-slug'>
          <FieldLabel htmlFor='role-slug'>Slug</FieldLabel>
          <Input
            id='role-slug'
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            placeholder='reviewers'
          />
          <FieldDescription>
            Lowercase letters, digits, dashes, underscores — never changes.
          </FieldDescription>
        </Field>
        <Field id='role-description'>
          <FieldLabel htmlFor='role-description'>Description (optional)</FieldLabel>
          <Input
            id='role-description'
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </Field>
        <DialogFooter>
          <Button variant='ghost' onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={creating.isPending} onClick={submit}>
            {creating.isPending ? <Spinner /> : null}
            Create role
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function EditRoleDialog({ role, onClose }: { role: Role; onClose: () => void }) {
  const [name, setName] = useState(role.name)
  const [description, setDescription] = useState(role.description ?? '')
  const [failed, setFailed] = useState<string | null>(null)
  const queryClient = useQueryClient()

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: AuthorizationService.method.listRoles,
        cardinality: 'finite'
      })
    })
  }

  const updating = useMutation(AuthorizationService.method.updateRole, {
    onSuccess: () => {
      setFailed(null)
      onClose()
      invalidate()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const submit = () => {
    setFailed(null)
    if (!name.trim()) {
      setFailed('Give the role a name.')
      return
    }
    updating.mutate(
      create(UpdateRoleRequestSchema, {
        id: role.id,
        name: name.trim(),
        description: description.trim()
      })
    )
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit {role.name}</DialogTitle>
          <DialogDescription>
            The slug {role.slug} never moves — the claims and the code reference it.
          </DialogDescription>
        </DialogHeader>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        ) : null}
        <Field id='edit-role-name'>
          <FieldLabel htmlFor='edit-role-name'>Name</FieldLabel>
          <Input id='edit-role-name' value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field id='edit-role-description'>
          <FieldLabel htmlFor='edit-role-description'>Description</FieldLabel>
          <Input
            id='edit-role-description'
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </Field>
        <DialogFooter>
          <Button variant='ghost' onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={updating.isPending} onClick={submit}>
            {updating.isPending ? <Spinner /> : null}
            Save role
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function DeleteRoleDialog({ role, onClose }: { role: Role; onClose: () => void }) {
  const [failed, setFailed] = useState<string | null>(null)
  const queryClient = useQueryClient()

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: AuthorizationService.method.listRoles,
        cardinality: 'finite'
      })
    })
  }

  const deleting = useMutation(AuthorizationService.method.deleteRole, {
    onSuccess: () => {
      onClose()
      invalidate()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <AlertDialog open onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {role.name}</AlertDialogTitle>
          <AlertDialogDescription>
            The role leaves the catalog. A role accounts still hold is refused — lift the grants
            first, so a deletion cannot silently strip the accounts that carried it.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant='destructive'
            disabled={deleting.isPending}
            onClick={(event) => {
              event.preventDefault()
              deleting.mutate(create(DeleteRoleRequestSchema, { id: role.id }))
            }}
          >
            {deleting.isPending ? <Spinner /> : null}
            Delete role
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/**
 * The permission tree over the catalog listing. The set saves as a whole
 * replacement; a system role's set is the seed's, so the tree opens
 * read-only. The copy names the custody rule: the accounts carrying the
 * role pick the changed set up at their next token.
 */
function PermissionsDialog({
  role,
  onClose
}: {
  role: Pick<Role, 'id' | 'name' | 'type'>
  onClose: () => void
}) {
  const system = role.type === RoleType.SYSTEM
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [failed, setFailed] = useState<string | null>(null)
  const queryClient = useQueryClient()

  const detail = useQuery(
    AuthorizationService.method.getRole,
    create(GetRoleRequestSchema, { id: role.id })
  )
  const catalog = useQuery(
    AuthorizationService.method.listPermissions,
    create(ListPermissionsRequestSchema, {})
  )

  // The tree seeds from the role's own set once the read answers; from
  // there the set is the operator's, and the save sends it whole.
  useEffect(() => {
    if (!detail.data) return
    setPicked(new Set(detail.data.role?.permissions ?? []))
  }, [detail.data])

  const grouped = useMemo(() => {
    const groups = new Map<string, { slug: string; description: string }[]>()
    for (const entry of catalog.data?.permissions ?? []) {
      const resource = entry.slug.split(':')[0] ?? 'other'
      const bucket = groups.get(resource) ?? []
      bucket.push({ slug: entry.slug, description: entry.description })
      groups.set(resource, bucket)
    }
    return [...groups.entries()]
  }, [catalog.data])

  const toggle = (slug: string, on: boolean) => {
    setPicked((current) => {
      const next = new Set(current)
      if (on) {
        next.add(slug)
      } else {
        next.delete(slug)
      }
      return next
    })
  }

  const saving = useMutation(AuthorizationService.method.setRolePermissions, {
    onSuccess: () => {
      setFailed(null)
      onClose()
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: AuthorizationService.method.listRoles,
          cardinality: 'finite'
        })
      })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const save = () => {
    setFailed(null)
    saving.mutate(
      create(SetRolePermissionsRequestSchema, { id: role.id, permissionSlugs: [...picked] })
    )
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent style={localStyles.treeDialog}>
        <DialogHeader>
          <DialogTitle>Permissions of {role.name}</DialogTitle>
          <DialogDescription>
            {system
              ? 'A system role carries the seed’s set; it is shown read-only here.'
              : 'The set saves as a whole replacement. The accounts carrying the role pick it up at their next token, not in place.'}
          </DialogDescription>
        </DialogHeader>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        ) : null}
        {detail.isPending || catalog.isPending ? (
          <FormSkeleton fields={3} />
        ) : (
          <div {...stylex.props(localStyles.tree)}>
            {grouped.map(([resource, entries]) => (
              <div key={resource} {...stylex.props(localStyles.treeGroup)}>
                <Text variant='body-2' weight='semibold'>
                  {resource.replaceAll('_', ' ')}
                </Text>
                {entries.map((entry) => (
                  <label key={entry.slug} {...stylex.props(localStyles.treeRow)}>
                    <Checkbox
                      checked={picked.has(entry.slug)}
                      onCheckedChange={(on) => toggle(entry.slug, on)}
                      disabled={system}
                      aria-label={`Permission: ${entry.slug}`}
                    />
                    <span {...stylex.props(localStyles.treeSlug)}>{entry.slug}</span>
                    <span {...stylex.props(localStyles.treeDescription)}>{entry.description}</span>
                  </label>
                ))}
              </div>
            ))}
          </div>
        )}
        <DialogFooter>
          <Button variant='ghost' onClick={onClose}>
            Close
          </Button>
          {!system ? (
            <Button disabled={saving.isPending} onClick={save}>
              {saving.isPending ? <Spinner /> : null}
              Save permissions
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

const localStyles = stylex.create({
  icon: {
    height: 16,
    width: 16
  },
  typeBadge: {
    marginInlineStart: '0.5rem'
  },
  sort: {
    minWidth: '10rem'
  },
  searchRow: {
    alignItems: 'center',
    display: 'flex',
    gap: '0.5rem',
    marginBottom: '1rem'
  },
  search: {
    flex: '1 1 14rem'
  },
  pager: {
    alignItems: 'center',
    display: 'flex',
    gap: '1rem',
    justifyContent: 'space-between'
  },
  pagerActions: {
    display: 'flex',
    gap: '0.5rem'
  },
  treeDialog: {
    maxWidth: '44rem',
    width: '90vw'
  },
  tree: {
    display: 'flex',
    flexDirection: 'column',
    gap: '1rem',
    maxHeight: '24rem',
    overflowY: 'auto'
  },
  treeGroup: {
    display: 'flex',
    flexDirection: 'column',
    gap: '0.375rem'
  },
  treeRow: {
    alignItems: 'center',
    display: 'flex',
    gap: '0.5rem'
  },
  treeSlug: {
    fontFamily: 'monospace',
    fontSize: '0.8125rem'
  },
  treeDescription: {
    color: 'var(--color-neutral-faded)',
    fontSize: '0.8125rem'
  }
})
