import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Plus } from '@keyline-icons/react'
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
  AlertDialogTitle
} from 'uilibs/components/base/alert-dialog'
import { Button } from 'uilibs/components/base/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from 'uilibs/components/base/tabs'
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
  ItemTitle
} from 'uilibs/components/extra/item'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { Textarea } from 'uilibs/components/extra/textarea'
import { PageHeader } from '#/components/-page-header'
import { LIST_MAX_LIMIT, listPageInput } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { Can } from '#/libraries/guard/can'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  CreateGroupCustomClaimRequestSchema,
  CreateUserCustomClaimRequestSchema,
  CustomClaimService,
  DeleteGroupCustomClaimRequestSchema,
  DeleteUserCustomClaimRequestSchema,
  ListGroupCustomClaimsRequestSchema,
  ListUserCustomClaimsRequestSchema,
  SuggestCustomClaimsRequestSchema,
  UpdateGroupCustomClaimRequestSchema,
  UpdateUserCustomClaimRequestSchema,
  type CustomClaim
} from '~/codegen/federation_pb'
import {
  ListUserGroupsRequestSchema,
  ListUsersRequestSchema,
  UserGroupService,
  UserService
} from '~/codegen/identity_pb'

/**
 * The custom claims screen (phase 2): the claims the deployment hangs on
 * accounts and groups, which the federation's tokens carry. The claims live
 * on a subject — the operator picks the account or group, then works on the
 * roll beneath it. The key suggestions are the deployment's own usage.
 */
export function ClaimsView() {
  const [userSubject, setUserSubject] = useState('')
  const [groupSubject, setGroupSubject] = useState('')

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
        title='Custom claims'
        description="The claims beyond the protocol's own — hung on accounts and groups, carried by every token the federation issues."
      />

      <div {...stylex.props(pageStyles.stack)}>
        <Tabs defaultValue='user'>
          <TabsList>
            <TabsTrigger value='user'>User claims</TabsTrigger>
            <TabsTrigger value='group'>Group claims</TabsTrigger>
          </TabsList>
          <TabsContent value='user'>
            <SubjectPickCard
              kind='user'
              title='Claims on an account'
              description="A group claim reaches the account through its membership; a claim here is the account's own."
              onSubject={(id) => setUserSubject(id ?? '')}
            />
            <ClaimsCard subjectId={userSubject} kind='user' />
          </TabsContent>
          <TabsContent value='group'>
            <SubjectPickCard
              kind='group'
              title='Claims on a group'
              description="Every member's tokens carry the group's claims — one write reaches a whole membership."
              onSubject={(id) => setGroupSubject(id ?? '')}
            />
            <ClaimsCard subjectId={groupSubject} kind='group' />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}

function SubjectPickCard({
  kind,
  title,
  description,
  onSubject
}: {
  kind: ClaimKind
  title: string
  description: string
  onSubject: (subjectId: string | undefined) => void
}) {
  const users = useQuery(
    UserService.method.listUsers,
    create(ListUsersRequestSchema, listPageInput(1, LIST_MAX_LIMIT)),
    { enabled: kind === 'user' }
  )
  const groups = useQuery(
    UserGroupService.method.listUserGroups,
    create(ListUserGroupsRequestSchema, listPageInput(1, LIST_MAX_LIMIT)),
    { enabled: kind === 'group' }
  )
  const usersList = kind === 'user' ? (users.data?.users ?? []) : []
  const groupsList = kind === 'group' ? (groups.data?.groups ?? []) : []

  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        <Select onValueChange={(next) => onSubject(typeof next === 'string' ? next : undefined)}>
          <SelectTrigger>
            <SelectValue placeholder='Choose the subject' />
          </SelectTrigger>
          <SelectContent>
            {usersList.map((user) => (
              <SelectItem key={user.id} value={user.id}>
                {user.displayName || user.username}
              </SelectItem>
            ))}
            {groupsList.map((group) => (
              <SelectItem key={group.id} value={group.id}>
                {group.displayName || group.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </CardContent>
    </Card>
  )
}

type ClaimKind = 'user' | 'group'

function ClaimsCard({ subjectId, kind }: { subjectId: string; kind: ClaimKind }) {
  const queryClient = useQueryClient()
  const [dialogClaim, setDialogClaim] = useState<CustomClaim | 'new' | null>(null)
  const [deleteClaim, setDeleteClaim] = useState<CustomClaim | null>(null)
  const [failed, setFailed] = useState<string | null>(null)

  // The two lists are separate queries — the wire's user and group claim
  // reads are different procedures with different request shapes, so the
  // hooks stay explicit and the kind only switches which one is enabled.
  const userClaims = useQuery(
    CustomClaimService.method.listUserClaims,
    create(ListUserCustomClaimsRequestSchema, { userId: subjectId }),
    { enabled: kind === 'user' && subjectId !== '' }
  )
  const groupClaims = useQuery(
    CustomClaimService.method.listGroupClaims,
    create(ListGroupCustomClaimsRequestSchema, { userGroupId: subjectId }),
    { enabled: kind === 'group' && subjectId !== '' }
  )
  const rows = (kind === 'user' ? userClaims.data : groupClaims.data)?.claims ?? []
  const pending = kind === 'user' ? userClaims.isPending : groupClaims.isPending

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: CustomClaimService.method.listUserClaims,
        cardinality: 'finite'
      })
    })
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: CustomClaimService.method.listGroupClaims,
        cardinality: 'finite'
      })
    })
  }

  const removeUser = useMutation(CustomClaimService.method.deleteUserClaim, {
    onSuccess: () => {
      setFailed(null)
      setDeleteClaim(null)
      invalidate()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })
  const removeGroup = useMutation(CustomClaimService.method.deleteGroupClaim, {
    onSuccess: () => {
      setFailed(null)
      setDeleteClaim(null)
      invalidate()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const withdraw = (claimId: string) => {
    if (kind === 'user') {
      removeUser.mutate(create(DeleteUserCustomClaimRequestSchema, { id: claimId }))
    } else {
      removeGroup.mutate(create(DeleteGroupCustomClaimRequestSchema, { id: claimId }))
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Claims</CardTitle>
        <CardDescription>
          The key is the claim's name in the token; the value is a plain string or a JSON document.
        </CardDescription>
        <CardAction>
          <Can permission='custom_claim:*:create'>
            <Button size='sm' disabled={subjectId === ''} onClick={() => setDialogClaim('new')}>
              <Plus size={16} />
              Add claim
            </Button>
          </Can>
        </CardAction>
      </CardHeader>
      <CardContent>
        {subjectId === '' ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No subject chosen</EmptyTitle>
              <EmptyDescription>
                Choose the {kind === 'user' ? 'account' : 'group'} whose claims to work on.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : pending ? (
          <Spinner />
        ) : rows.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No claims</EmptyTitle>
              <EmptyDescription>
                Hang the first claim on this {kind === 'user' ? 'account' : 'group'}.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ItemGroup>
            {rows.map((claim) => (
              <Item key={claim.id} variant='outline'>
                <ItemContent>
                  <ItemTitle>{claim.key}</ItemTitle>
                  <ItemDescription>{claim.value}</ItemDescription>
                </ItemContent>
                <ItemActions>
                  <Can permission='custom_claim:*:update'>
                    <Button variant='outline' size='sm' onClick={() => setDialogClaim(claim)}>
                      Edit
                    </Button>
                  </Can>
                  <Can permission='custom_claim:*:delete'>
                    <Button variant='outline' size='sm' onClick={() => setDeleteClaim(claim)}>
                      Delete
                    </Button>
                  </Can>
                </ItemActions>
              </Item>
            ))}
          </ItemGroup>
        )}

        {failed ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        ) : null}
      </CardContent>

      <ClaimDialog
        key={dialogClaim === 'new' ? 'new' : (dialogClaim?.id ?? 'none')}
        kind={kind}
        subjectId={subjectId}
        claim={dialogClaim === 'new' ? null : dialogClaim}
        open={dialogClaim !== null}
        onOpenChange={(next) => {
          if (!next) setDialogClaim(null)
        }}
        onDone={() => {
          setDialogClaim(null)
          invalidate()
        }}
      />

      <AlertDialog
        open={deleteClaim !== null}
        onOpenChange={(next) => {
          if (!next) setDeleteClaim(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete the claim {deleteClaim?.key}?</AlertDialogTitle>
            <AlertDialogDescription>
              The tokens issued after the deletion stop carrying it. This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (!deleteClaim) return
                withdraw(deleteClaim.id)
              }}
            >
              Delete claim
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  )
}

/** The key suggestions: the deployment's own usage, most-used first. */
function SuggestChips({ onPick }: { onPick: (key: string) => void }) {
  const suggestions = useQuery(
    CustomClaimService.method.suggest,
    create(SuggestCustomClaimsRequestSchema, {})
  )
  const keys = suggestions.data?.keys ?? []
  if (keys.length === 0) return null

  return (
    <div {...stylex.props(localStyles.chips)}>
      {keys.slice(0, 8).map((entry) => (
        <Button key={entry.key} variant='outline' size='sm' onClick={() => onPick(entry.key)}>
          {entry.key} ({entry.usageCount})
        </Button>
      ))}
    </div>
  )
}

function ClaimDialog({
  kind,
  subjectId,
  claim,
  open,
  onOpenChange,
  onDone
}: {
  kind: ClaimKind
  subjectId: string
  claim: CustomClaim | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const [failed, setFailed] = useState<string | null>(null)
  const [key, setKey] = useState(claim?.key ?? '')
  const [value, setValue] = useState(claim?.value ?? '')

  // Four mutations, two per kind — the wire's user and group claim writes
  // are separate procedures, so the hooks stay explicit (a union of method
  // descriptors does not type-check).
  const createUser = useMutation(CustomClaimService.method.createUserClaim, {
    onSuccess: () => {
      setFailed(null)
      onDone()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })
  const createGroup = useMutation(CustomClaimService.method.createGroupClaim, {
    onSuccess: () => {
      setFailed(null)
      onDone()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })
  const updateUser = useMutation(CustomClaimService.method.updateUserClaim, {
    onSuccess: () => {
      setFailed(null)
      onDone()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })
  const updateGroup = useMutation(CustomClaimService.method.updateGroupClaim, {
    onSuccess: () => {
      setFailed(null)
      onDone()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })
  const busy =
    createUser.isPending || createGroup.isPending || updateUser.isPending || updateGroup.isPending

  const submit = () => {
    if (!key.trim() || !value.trim()) {
      setFailed('The claim needs a key and a value.')
      return
    }
    if (claim && kind === 'user') {
      updateUser.mutate(
        create(UpdateUserCustomClaimRequestSchema, { id: claim.id, key: key.trim(), value })
      )
    } else if (claim) {
      updateGroup.mutate(
        create(UpdateGroupCustomClaimRequestSchema, { id: claim.id, key: key.trim(), value })
      )
    } else if (kind === 'user') {
      createUser.mutate(
        create(CreateUserCustomClaimRequestSchema, { userId: subjectId, key: key.trim(), value })
      )
    } else {
      createGroup.mutate(
        create(CreateGroupCustomClaimRequestSchema, {
          userGroupId: subjectId,
          key: key.trim(),
          value
        })
      )
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setFailed(null)
          setKey('')
          setValue('')
        }
        onOpenChange(next)
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{claim ? 'Edit the claim' : 'Add a claim'}</DialogTitle>
          <DialogDescription>
            {claim
              ? 'The rewrite takes effect at the next token.'
              : 'The claim rides the tokens the federation issues.'}
          </DialogDescription>
        </DialogHeader>
        <Field>
          <FieldLabel>Key</FieldLabel>
          <Input value={key} onChange={(event) => setKey(event.target.value)} />
          <SuggestChips onPick={setKey} />
          <FieldDescription>
            A name the protocol does not reserve — the suggestions are the deployment's own usage.
          </FieldDescription>
        </Field>
        <Field>
          <FieldLabel>Value</FieldLabel>
          <Textarea
            value={value}
            onChange={(event) => setValue(event.target.value)}
            aria-label='Value'
            autoResize
          />
          <FieldDescription>A plain string, or a JSON document the tokens carry.</FieldDescription>
        </Field>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        ) : null}
        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={busy} onClick={submit}>
            {claim ? 'Save claim' : 'Add claim'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

const localStyles = stylex.create({
  chips: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 8,
    marginTop: 8
  }
})
