import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Ban, Clock, Delete, Eye, KeyRound, MailX, ShieldOff } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
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
import { Field, FieldDescription, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { Switch } from 'uilibs/components/base/switch'
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
  DescriptionDetails,
  DescriptionList,
  DescriptionTerm
} from 'uilibs/components/extra/description-list'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { DateTimePicker } from '#/components/-date-time-picker'
import { FormSkeleton } from '#/components/-skeleton-form'
import { ListPagination } from '#/components/list-pagination'
import { UserAvatar } from '#/components/user-avatar'
import { useAuthentication } from '#/hooks/use-auth'
import { listPageInput, usePaginatedList, LIST_MAX_LIMIT } from '#/hooks/use-pagination'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { Can } from '#/libraries/guard/can'
import { pageStyles } from '#/styles/pages/page.stylex'
import { AuditLogService, ListForUserRequestSchema } from '~/codegen/auditlog_pb'
import {
  AdminDisableMfaRequestSchema,
  MultifactorService,
  PasswordRecoveryService,
  AdminResetUserPasswordRequestSchema
} from '~/codegen/authn_pb'
import {
  AuthorizationService,
  ListPermissionsRequestSchema,
  ListRolesRequestSchema,
  ListUserPermissionsRequestSchema,
  ListUserRolesRequestSchema,
  SetUserPermissionsRequestSchema,
  SetUserRolesRequestSchema
} from '~/codegen/authz_pb'
import {
  BanUserRequestSchema,
  DeleteUserRequestSchema,
  GetUserRequestSchema,
  UnlockUserRequestSchema,
  UnbanUserRequestSchema,
  UpdateUserRequestSchema,
  UserService,
  type User
} from '~/codegen/identity_pb'
import {
  AdminDeleteCredentialRequestSchema,
  AdminListCredentialsRequestSchema,
  AdminUpdateCredentialRequestSchema,
  WebAuthnService,
  type Credential
} from '~/codegen/webauthn_pb'
import { AuditTable } from '../-audit-view'

const localStyles = stylex.create({
  form: {
    display: 'flex',
    flexDirection: 'column',
    gap: 16,
    maxWidth: '28rem'
  },
  metaRow: {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: 8
  },
  credentialRow: {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: 8
  },
  credentialMeta: {
    flex: 1,
    minWidth: 0
  },
  actions: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 8
  },
  error: {
    marginTop: 8
  },
  activityPager: {
    alignItems: 'center',
    display: 'flex',
    gap: '1rem',
    justifyContent: 'space-between',
    marginTop: '1rem'
  },
  pagerActions: {
    display: 'flex',
    gap: '0.5rem'
  },
  grantList: {
    display: 'flex',
    flexDirection: 'column',
    gap: '0.5rem',
    marginBlock: '0.5rem 1rem'
  },
  grantRow: {
    alignItems: 'center',
    display: 'flex',
    gap: '0.5rem'
  },
  grantSection: {
    display: 'block',
    marginTop: '0.5rem'
  }
})

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value: string | undefined | null): string {
  if (!value) return '—'
  const instant = new Date(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

function formatWireInstant(value: Timestamp | undefined): string {
  if (!value) return '—'
  const instant = timestampDate(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/** An RFC 3339 instant the wire answered with, as the init the message
 * field's shape folds into a protobuf Timestamp. */
function wireTimestamp(value: string): { seconds: bigint } {
  const seconds = Math.floor(Date.parse(value) / 1000)
  return { seconds: BigInt(seconds) }
}

/**
 * The user management center's detail page (D4): the full-replace update
 * form, the ban as the unit it is on the wire, the credential management
 * the roll answers, and the destructive actions in their confirmations.
 * The server's refusals — delete-self, an administrator target, a cooldown —
 * read verbatim; no permission word is invented client-side.
 */
/** The search a navigation carries when it means "the list's defaults" —
 * every key optional on the route, but the typed Link wants them named. */
const DEFAULT_USERS_SEARCH = {
  page: undefined,
  search: undefined,
  sortBy: undefined,
  sortOrder: undefined
} as const

export function UserDetailView({ userId }: { userId: string }) {
  const navigate = useNavigate()
  const { impersonateUser } = useAuthentication()
  const queryClient = useQueryClient()

  const read = useQuery(UserService.method.getUser, create(GetUserRequestSchema, { id: userId }))
  const user = read.data?.user

  const invalidateUser = () => {
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: UserService.method.getUser,
        input: create(GetUserRequestSchema, { id: userId }),
        cardinality: 'finite'
      })
    })
  }

  const remove = useMutation(UserService.method.deleteUser, {
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: UserService.method.listUsers,
          cardinality: 'finite'
        })
      })
      void navigate({ to: '/admin/users', search: DEFAULT_USERS_SEARCH })
    },
    onError: () => {
      // The refusal reads inside the confirmation — the dialog stays open
      // and owns the sentence.
    }
  })

  if (read.isPending) {
    return (
      <div
        {...stylex.props(
          pageStyles.container,
          pageStyles.containerPadMedium,
          pageStyles.containerPadLarge,
          pageStyles.containerPadXLarge
        )}
      >
        <FormSkeleton fields={4} />
      </div>
    )
  }
  if (read.isError) {
    return (
      <div {...stylex.props(pageStyles.container, pageStyles.containerPadMedium)}>
        <Text render={<p />} variant='body-2' color='critical'>
          {getErrorMessage(read.error)}
        </Text>
      </div>
    )
  }
  if (!user) return null

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
            {user.displayName || user.username}
          </Text>
          <DescriptionList>
            <DescriptionTerm>Username</DescriptionTerm>
            <DescriptionDetails>{user.username}</DescriptionDetails>
            <DescriptionTerm>Email</DescriptionTerm>
            <DescriptionDetails>{user.email}</DescriptionDetails>
            <DescriptionTerm>Joined</DescriptionTerm>
            <DescriptionDetails>{formatInstant(user.createdAt)}</DescriptionDetails>
            <DescriptionTerm>Identifier</DescriptionTerm>
            <DescriptionDetails>{user.id}</DescriptionDetails>
            <DescriptionTerm>Status</DescriptionTerm>
            <DescriptionDetails>
              {user.bannedAt ? <Badge variant='destructive'>Banned</Badge> : null}
              {user.disabled ? <Badge variant='secondary'>Disabled</Badge> : null}
              {!user.emailVerified ? <Badge variant='secondary'>Unverified</Badge> : null}
              {!user.bannedAt && !user.disabled && user.emailVerified ? (
                <Badge variant='secondary'>Active</Badge>
              ) : null}
            </DescriptionDetails>
          </DescriptionList>
        </div>
        <CardAction>
          <UserAvatar name={user.displayName || user.username} picture={user.picture} size='lg' />
        </CardAction>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <ProfileCard user={user} onSaved={() => invalidateUser()} />
        <BanCard user={user} onSettled={() => invalidateUser()} />
        <SecurityCard user={user} onSettled={() => invalidateUser()} />
        <DelegationCard onImpersonate={(reason) => impersonateUser(user.id, reason)} />
        <ActivityCard userId={user.id} />
        <GrantsCard user={user} />
        <DangerCard
          onDelete={() => remove.mutate(create(DeleteUserRequestSchema, { id: user.id }))}
          pending={remove.isPending}
          error={remove.error ? getErrorMessage(remove.error) : null}
        />
      </div>
    </div>
  )
}

/** The full-replace profile form. The ban fields are not this card's — a
 * profile save carries the account's current ban expiry untouched, because
 * the wire treats the ban fields as one unit and a save must not lift what
 * it did not mean to touch. */
function ProfileCard({ user, onSaved }: { user: User; onSaved: () => void }) {
  const [failed, setFailed] = useState<string | null>(null)
  const [form, setForm] = useState({
    username: user.username,
    email: user.email,
    firstName: user.firstName ?? '',
    lastName: user.lastName ?? '',
    displayName: user.displayName,
    locale: user.metadata?.locale ?? '',
    disabled: user.disabled
  })

  const update = useMutation(UserService.method.updateUser, {
    onSuccess: () => {
      setFailed(null)
      onSaved()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const submit = () => {
    setFailed(null)
    update.mutate(
      create(UpdateUserRequestSchema, {
        id: user.id,
        username: form.username.trim(),
        email: form.email.trim(),
        firstName: form.firstName.trim(),
        lastName: form.lastName.trim(),
        displayName: form.displayName.trim(),
        locale: form.locale.trim(),
        disabled: form.disabled,
        // The ban rides untouched: a banned account keeps its expiry (and
        // its reason), an unbanned one sends none — the wire's unit.
        banExpires: user.banExpires ? wireTimestamp(user.banExpires) : undefined,
        banReason: user.banExpires ? (user.banReason ?? '') : undefined
      })
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Profile</CardTitle>
        <CardDescription>
          The write is a full replace: an empty locale clears its column.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div {...stylex.props(localStyles.form)}>
          <Field>
            <FieldLabel>Username</FieldLabel>
            <Input
              value={form.username}
              onChange={(event) => setForm({ ...form, username: event.target.value })}
              aria-label='Username'
            />
          </Field>
          <Field>
            <FieldLabel>Email</FieldLabel>
            <Input
              type='email'
              value={form.email}
              onChange={(event) => setForm({ ...form, email: event.target.value })}
              aria-label='Email'
            />
          </Field>
          <Field>
            <FieldLabel>First name</FieldLabel>
            <Input
              value={form.firstName}
              onChange={(event) => setForm({ ...form, firstName: event.target.value })}
              aria-label='First name'
            />
          </Field>
          <Field>
            <FieldLabel>Last name</FieldLabel>
            <Input
              value={form.lastName}
              onChange={(event) => setForm({ ...form, lastName: event.target.value })}
              aria-label='Last name'
            />
          </Field>
          <Field>
            <FieldLabel>Display name</FieldLabel>
            <Input
              value={form.displayName}
              onChange={(event) => setForm({ ...form, displayName: event.target.value })}
              aria-label='Display name'
            />
          </Field>
          <Field>
            <FieldLabel>Locale</FieldLabel>
            <Input
              value={form.locale}
              onChange={(event) => setForm({ ...form, locale: event.target.value })}
              aria-label='Locale'
              placeholder='e.g. en-GB — empty clears it'
            />
          </Field>
          <Field>
            <div {...stylex.props(localStyles.metaRow)}>
              <Switch
                checked={form.disabled}
                onCheckedChange={(checked) => setForm({ ...form, disabled: checked })}
                aria-label='Refuse sign-ins'
              />
              <FieldLabel>Refuse sign-ins</FieldLabel>
            </div>
            <FieldDescription>A disabled account cannot sign in until released.</FieldDescription>
          </Field>
          {failed ? (
            <Text render={<p />} variant='body-2' color='critical'>
              {failed}
            </Text>
          ) : null}
          <div>
            <Button
              size='sm'
              disabled={
                update.isPending ||
                form.username.trim().length === 0 ||
                form.email.trim().length === 0 ||
                form.displayName.trim().length === 0 ||
                form.firstName.trim().length === 0 ||
                form.lastName.trim().length === 0
              }
              onClick={submit}
            >
              {update.isPending ? <Spinner /> : null}
              Save changes
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

/** The ban as the wire's unit: an expiry present applies it, absent lifts
 * it. The unlock action is here because the account row cannot say whether
 * an open lockout stands — the procedure answers the same success when none
 * does, and the sentence says so. */
function BanCard({ user, onSettled }: { user: User; onSettled: () => void }) {
  const banned = Boolean(user.bannedAt)

  return (
    <Card>
      <CardHeader>
        <CardTitle>Ban</CardTitle>
        <CardDescription>
          {banned
            ? `Since ${formatInstant(user.bannedAt)}${user.banExpires ? `, lifts ${formatInstant(user.banExpires)}` : ' — no expiry set'}.`
            : 'The account is not banned.'}
        </CardDescription>
        {banned && user.banReason ? (
          <CardDescription>Reason: {user.banReason}</CardDescription>
        ) : null}
      </CardHeader>
      <CardContent>
        <div {...stylex.props(localStyles.actions)}>
          {banned ? (
            <Can permission='user:*:unban'>
              <UnbanDialog onSettled={onSettled} />
            </Can>
          ) : (
            <Can permission='user:*:ban'>
              <BanDialog onSettled={onSettled} />
            </Can>
          )}
          <Can permission='user:*:update'>
            <UnlockDialog onSettled={onSettled} />
          </Can>
        </div>
      </CardContent>
    </Card>
  )
}

function BanDialog({ onSettled }: { onSettled: () => void }) {
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [reason, setReason] = useState('')
  const [expires, setExpires] = useState<Date | undefined>(undefined)

  const ban = useMutation(UserService.method.banUser, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      setReason('')
      setExpires(undefined)
      onSettled()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const submit = () => {
    setFailed(null)
    const parsed = expires && !Number.isNaN(expires.getTime()) ? expires : null
    const instant = parsed ? wireTimestamp(parsed.toISOString()) : undefined
    ban.mutate(
      create(BanUserRequestSchema, {
        reason: reason.trim(),
        expiresAt: instant
      })
    )
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger render={<Button variant='outline' size='sm' />}>
        <Ban size={16} /> Ban
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Ban this account?</AlertDialogTitle>
          <AlertDialogDescription>
            Every live session the account holds ends in the same write, and the account's address
            learns of the ban. An expiry left empty never lifts by itself.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Field id='ban-reason'>
          <FieldLabel htmlFor='ban-reason'>Reason</FieldLabel>
          <Input
            id='ban-reason'
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
          <FieldDescription>
            The sentence the audit record and the notification keep.
          </FieldDescription>
        </Field>
        <Field id='ban-expiry'>
          <FieldLabel htmlFor='ban-expiry'>Expires at</FieldLabel>
          <DateTimePicker
            value={expires}
            onChange={setExpires}
            id='ban-expiry'
            ariaLabel='Expires at'
            placeholder='Empty — the ban never lifts by itself'
          />
        </Field>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
            {failed}
          </Text>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant='destructive'
            disabled={ban.isPending || reason.trim().length === 0}
            onClick={submit}
          >
            {ban.isPending ? <Spinner /> : <Ban size={16} />}
            Ban
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function UnbanDialog({ onSettled }: { onSettled: () => void }) {
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)

  const unban = useMutation(UserService.method.unbanUser, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      onSettled()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger render={<Button variant='outline' size='sm' />}>
        <ShieldOff size={16} /> Lift ban
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Lift the ban?</AlertDialogTitle>
          <AlertDialogDescription>
            The start instant, the expiry, and the reason lift together, and the account's address
            learns of the lift.
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
            disabled={unban.isPending}
            onClick={() => {
              setFailed(null)
              unban.mutate(create(UnbanUserRequestSchema, {}))
            }}
          >
            {unban.isPending ? <Spinner /> : <ShieldOff size={16} />}
            Lift ban
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function UnlockDialog({ onSettled }: { onSettled: () => void }) {
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)

  const unlock = useMutation(UserService.method.unlockUser, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      onSettled()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger render={<Button variant='outline' size='sm' />}>
        <Clock size={16} /> Unlock
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Lift the lockout?</AlertDialogTitle>
          <AlertDialogDescription>
            An open lockout lifts and the failed-attempt streak starts fresh. A ban is not a lockout
            — an account with none standing is the same success.
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
            disabled={unlock.isPending}
            onClick={() => {
              setFailed(null)
              unlock.mutate(create(UnlockUserRequestSchema, {}))
            }}
          >
            {unlock.isPending ? <Spinner /> : <Clock size={16} />}
            Unlock
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** The credential management the roll answers: the password reset ask with
 * its four real states, the second factor's removal, and the passkey list
 * with its rename and removal. */
function SecurityCard({ user, onSettled }: { user: User; onSettled: () => void }) {
  const credentials = useQuery(
    WebAuthnService.method.adminListCredentials,
    create(AdminListCredentialsRequestSchema, { userId: user.id })
  )

  return (
    <Card>
      <CardHeader>
        <CardTitle>Security</CardTitle>
        <CardDescription>
          The credentials the account signs in with, managed from here — the secrets never leave the
          account's own devices.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div {...stylex.props(pageStyles.stack)}>
          <div {...stylex.props(localStyles.metaRow)}>
            <Text variant='body-2' color='neutral-faded' style={localStyles.credentialMeta}>
              {user.passwordUpdatedAt
                ? `Password credential — last set ${formatInstant(user.passwordUpdatedAt)}.`
                : 'The account holds no password credential.'}
            </Text>
            <ResetPasswordDialog hasPassword={Boolean(user.passwordUpdatedAt)} />
          </div>
          <div {...stylex.props(localStyles.metaRow)}>
            <Text variant='body-2' color='neutral-faded' style={localStyles.credentialMeta}>
              The second factor is the account's to keep — or to lose from here.
            </Text>
            <DisableMfaDialog onSettled={onSettled} />
          </div>
          <div {...stylex.props(pageStyles.stack)}>
            {credentials.isPending ? (
              <Spinner />
            ) : (credentials.data?.credentials ?? []).length === 0 ? (
              <Text variant='body-2' color='neutral-faded'>
                No passkeys enrolled on this account.
              </Text>
            ) : (
              (credentials.data?.credentials ?? []).map((credential) => (
                <CredentialRow
                  key={credential.id}
                  userId={user.id}
                  credential={credential}
                  onSettled={onSettled}
                />
              ))
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

/** The four states the service answers (D8): not-found, no password
 * credential, the resend cooldown, and an unavailable mailer. The token
 * goes to the account's address, never to the caller — the answer's own
 * sentence is the whole confirmation. */
function ResetPasswordDialog({ hasPassword }: { hasPassword: boolean }) {
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [answered, setAnswered] = useState<string | null>(null)

  const ask = useMutation(PasswordRecoveryService.method.adminResetUserPassword, {
    onSuccess: (response) => setAnswered(response.message),
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger render={<Button variant='outline' size='sm' />} disabled={!hasPassword}>
        <MailX size={16} /> Email a reset
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Trigger a password reset?</AlertDialogTitle>
          <AlertDialogDescription>
            The reset token goes to the account's address — you never see or set the credential. The
            same cooldown the self-service keeps applies to your impatience.
          </AlertDialogDescription>
          {answered ? (
            <Text render={<p />} variant='body-2'>
              {answered}
            </Text>
          ) : null}
          {failed ? (
            <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
              {failed}
            </Text>
          ) : null}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Close</AlertDialogCancel>
          <AlertDialogAction
            disabled={ask.isPending}
            onClick={() => {
              setFailed(null)
              setAnswered(null)
              ask.mutate(create(AdminResetUserPasswordRequestSchema, {}))
            }}
          >
            {ask.isPending ? <Spinner /> : <MailX size={16} />}
            Email a reset
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function DisableMfaDialog({ onSettled }: { onSettled: () => void }) {
  const [open, setOpen] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [reason, setReason] = useState('')

  const disable = useMutation(MultifactorService.method.adminDisableMfa, {
    onSuccess: () => {
      setOpen(false)
      setFailed(null)
      setReason('')
      onSettled()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger render={<Button variant='outline' size='sm' />}>
        <ShieldOff size={16} /> Disable MFA
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove the second factor?</AlertDialogTitle>
          <AlertDialogDescription>
            Every enrolled authenticator and its recovery codes go with it, and the account's
            address learns of the removal.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Field id='mfa-reason'>
          <FieldLabel htmlFor='mfa-reason'>Reason</FieldLabel>
          <Input
            id='mfa-reason'
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
          <FieldDescription>
            The sentence the audit record and the notification keep.
          </FieldDescription>
        </Field>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
            {failed}
          </Text>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant='destructive'
            disabled={disable.isPending || reason.trim().length === 0}
            onClick={() => {
              setFailed(null)
              disable.mutate(
                create(AdminDisableMfaRequestSchema, { reason: reason.trim() || undefined })
              )
            }}
          >
            {disable.isPending ? <Spinner /> : <ShieldOff size={16} />}
            Disable MFA
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function CredentialRow({
  userId,
  credential,
  onSettled
}: {
  userId: string
  credential: Credential
  onSettled: () => void
}) {
  const [renaming, setRenaming] = useState(false)
  const [renameValue, setRenameValue] = useState(credential.name)
  const [renameFailed, setRenameFailed] = useState<string | null>(null)
  const [removing, setRemoving] = useState(false)
  const [removeFailed, setRemoveFailed] = useState<string | null>(null)

  const rename = useMutation(WebAuthnService.method.adminUpdateCredential, {
    onSuccess: () => {
      setRenaming(false)
      setRenameFailed(null)
      onSettled()
    },
    onError: (error) => setRenameFailed(getErrorMessage(error))
  })

  const remove = useMutation(WebAuthnService.method.adminDeleteCredential, {
    onSuccess: () => {
      setRemoving(false)
      setRemoveFailed(null)
      onSettled()
    },
    onError: (error) => setRemoveFailed(getErrorMessage(error))
  })

  return (
    <div {...stylex.props(localStyles.credentialRow)}>
      <KeyRound size={16} />
      <div {...stylex.props(localStyles.credentialMeta)}>
        <Text render={<p />} variant='body-2' weight='medium'>
          {credential.name}
        </Text>
        <Text variant='caption-1' color='neutral-faded'>
          Enrolled {formatWireInstant(credential.createdAt)}
          {credential.lastUsedAt
            ? ` · last used ${formatWireInstant(credential.lastUsedAt)}`
            : ' · never used'}
        </Text>
      </div>
      <AlertDialog
        open={renaming}
        onOpenChange={(next) => {
          setRenaming(next)
          if (next) setRenameValue(credential.name)
        }}
      >
        <AlertDialogTrigger render={<Button variant='outline' size='xs' />}>
          Rename
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Rename this passkey?</AlertDialogTitle>
            <AlertDialogDescription>
              The name is the display name the account holder chose — or the authenticator model.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <Field id='passkey-name'>
            <FieldLabel htmlFor='passkey-name'>Name</FieldLabel>
            <Input
              id='passkey-name'
              value={renameValue}
              onChange={(event) => setRenameValue(event.target.value)}
            />
          </Field>
          {renameFailed ? (
            <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
              {renameFailed}
            </Text>
          ) : null}
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={rename.isPending || renameValue.trim().length === 0}
              onClick={() =>
                rename.mutate(
                  create(AdminUpdateCredentialRequestSchema, {
                    userId,
                    credentialId: credential.id,
                    name: renameValue.trim()
                  })
                )
              }
            >
              {rename.isPending ? <Spinner /> : null}
              Rename
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <AlertDialog open={removing} onOpenChange={setRemoving}>
        <AlertDialogTrigger render={<Button variant='outline' size='xs' />}>
          Remove
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove this passkey?</AlertDialogTitle>
            <AlertDialogDescription>
              The credential stops signing for the account the moment the write lands.
            </AlertDialogDescription>
            {removeFailed ? (
              <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
                {removeFailed}
              </Text>
            ) : null}
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant='destructive'
              disabled={remove.isPending}
              onClick={() =>
                remove.mutate(
                  create(AdminDeleteCredentialRequestSchema, {
                    userId,
                    credentialId: credential.id
                  })
                )
              }
            >
              {remove.isPending ? <Spinner /> : <Delete size={16} />}
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

/** The delegation and the removal. The impersonation is a navigation, not a
 * confirmation — the banner is its own evidence — and the reason is the
 * sentence the audit row keeps. */ function DelegationCard({
  onImpersonate
}: {
  onImpersonate: (reason: string) => Promise<void>
}) {
  const [failed, setFailed] = useState<string | null>(null)
  const [reason, setReason] = useState('')

  const start = async () => {
    setFailed(null)
    try {
      await onImpersonate(reason.trim())
    } catch (error) {
      setFailed(getErrorMessage(error))
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Act as this account</CardTitle>
        <CardDescription>
          A new session opens on this account with a one-hour window; your own session is behind the
          banner. An administrator target is refused without confirming the role.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div {...stylex.props(localStyles.form)}>
          <Field id='impersonation-reason'>
            <FieldLabel htmlFor='impersonation-reason'>Reason</FieldLabel>
            <Input
              id='impersonation-reason'
              value={reason}
              onChange={(event) => setReason(event.target.value)}
            />
            <FieldDescription>
              What you were investigating — the audit row stores it.
            </FieldDescription>
          </Field>
          {failed ? (
            <Text render={<p />} variant='body-2' color='critical'>
              {failed}
            </Text>
          ) : null}
          <div>
            <Button size='sm' disabled={reason.trim().length === 0} onClick={() => void start()}>
              <Eye size={16} /> Start impersonating
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function DangerCard({
  onDelete,
  pending,
  error
}: {
  onDelete: () => void
  pending: boolean
  error: string | null
}) {
  const [open, setOpen] = useState(false)

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <Can permission='user:*:delete'>
        <AlertDialogTrigger render={<Button variant='outline' size='sm' />}>
          <Delete size={16} /> Delete account
        </AlertDialogTrigger>
      </Can>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete this account?</AlertDialogTitle>
          <AlertDialogDescription>
            The row is archived by the database trigger and the live sessions die with it. You
            cannot delete the account you are signed in with.
          </AlertDialogDescription>
          {error ? (
            <Text render={<p />} variant='body-2' color='critical' style={localStyles.error}>
              {error}
            </Text>
          ) : null}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant='destructive' disabled={pending} onClick={onDelete}>
            {pending ? <Spinner /> : <Delete size={16} />}
            Delete account
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/**
 * The account's grants (phase 6, D7): the roles the account carries and the
 * permissions granted directly, each as a checkbox set over the catalog and
 * saved as a whole replacement (`SetUserRoles`, `SetUserPermissions`). The
 * copy names the custody rule — grants move at the account's next token,
 * not in place — and when the operator edits their own account the screen
 * says so too, because the server refuses nothing there. The editing
 * controls sit behind the assignment grants; a caller without them reads
 * the card only.
 */
/** Toggle one slug in an immutable checkbox set; shared by the two sets. */
function toggleSlug(
  set: Set<string>,
  slug: string,
  on: boolean,
  apply: (next: Set<string>) => void
) {
  const next = new Set(set)
  if (on) {
    next.add(slug)
  } else {
    next.delete(slug)
  }
  apply(next)
}

function GrantsCard({ user }: { user: User }) {
  const { user: viewer } = useAuthentication()
  const editingSelf = viewer?.id === user.id
  const [failed, setFailed] = useState<string | null>(null)
  const [rolePicked, setRolePicked] = useState<Set<string>>(new Set())
  const [permissionPicked, setPermissionPicked] = useState<Set<string>>(new Set())

  const heldRoles = useQuery(
    AuthorizationService.method.listUserRoles,
    create(ListUserRolesRequestSchema, { userId: user.id })
  )
  const directGrants = useQuery(
    AuthorizationService.method.listUserPermissions,
    create(ListUserPermissionsRequestSchema, { userId: user.id })
  )
  const roles = useQuery(
    AuthorizationService.method.listRoles,
    create(ListRolesRequestSchema, { ...listPageInput(1, LIST_MAX_LIMIT) })
  )
  const catalog = useQuery(
    AuthorizationService.method.listPermissions,
    create(ListPermissionsRequestSchema, {})
  )

  // Each set seeds from its read once; from there the set is the
  // operator's, and the save sends it whole.
  useEffect(() => {
    if (!heldRoles.data) return
    setRolePicked(new Set((heldRoles.data.roles ?? []).map((role) => role.id)))
  }, [heldRoles.data])

  useEffect(() => {
    if (!directGrants.data) return
    setPermissionPicked(new Set(directGrants.data.permissionSlugs ?? []))
  }, [directGrants.data])

  const saveRoles = useMutation(AuthorizationService.method.setUserRoles, {
    onSuccess: () => {
      setFailed(null)
      void heldRoles.refetch()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })
  const savePermissions = useMutation(AuthorizationService.method.setUserPermissions, {
    onSuccess: () => {
      setFailed(null)
      void directGrants.refetch()
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>Grants</CardTitle>
        <CardDescription>
          {editingSelf
            ? 'You are editing your own grants. They move at your next token, not in place — the server refuses nothing here, so this note is the honesty.'
            : 'Grants move at this account’s next token, not in place.'}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {failed ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {failed}
          </Text>
        ) : null}

        <Text variant='body-2' weight='semibold'>
          Roles
        </Text>
        {heldRoles.isPending || roles.isPending ? (
          <Spinner />
        ) : (
          <div {...stylex.props(localStyles.grantList)}>
            {(roles.data?.roles ?? []).map((role) => (
              <label key={role.id} {...stylex.props(localStyles.grantRow)}>
                <Can permission='user:*:assign_role'>
                  <Checkbox
                    checked={rolePicked.has(role.id)}
                    onCheckedChange={(on) => toggleSlug(rolePicked, role.id, on, setRolePicked)}
                    aria-label={`Role: ${role.name}`}
                  />
                </Can>
                <span>
                  {role.name}
                  <Text variant='caption-1' color='neutral-faded'>
                    {' '}
                    ({role.slug}) · {role.permissionCount} permission
                    {role.permissionCount === 1 ? '' : 's'}
                  </Text>
                </span>
              </label>
            ))}
          </div>
        )}
        <Can permission='user:*:assign_role'>
          <Button
            variant='outline'
            size='sm'
            disabled={saveRoles.isPending}
            onClick={() =>
              saveRoles.mutate(
                create(SetUserRolesRequestSchema, { userId: user.id, roleIds: [...rolePicked] })
              )
            }
          >
            {saveRoles.isPending ? <Spinner /> : null}
            Save roles
          </Button>
        </Can>

        <Text variant='body-2' weight='semibold' style={localStyles.grantSection}>
          Direct permissions
        </Text>
        {directGrants.isPending || catalog.isPending ? (
          <Spinner />
        ) : (
          <div {...stylex.props(localStyles.grantList)}>
            {(catalog.data?.permissions ?? []).map((entry) => (
              <label key={entry.slug} {...stylex.props(localStyles.grantRow)}>
                <Can permission='user:*:grant_permission'>
                  <Checkbox
                    checked={permissionPicked.has(entry.slug)}
                    onCheckedChange={(on) =>
                      toggleSlug(permissionPicked, entry.slug, on, setPermissionPicked)
                    }
                    aria-label={`Grant: ${entry.slug}`}
                  />
                </Can>
                <span>
                  <Text render={<code />} variant='caption-1'>
                    {entry.slug}
                  </Text>
                  {entry.description ? (
                    <Text variant='caption-1' color='neutral-faded'>
                      {' '}
                      · {entry.description}
                    </Text>
                  ) : null}
                </span>
              </label>
            ))}
          </div>
        )}
        <Can permission='user:*:grant_permission'>
          <Button
            variant='outline'
            size='sm'
            disabled={savePermissions.isPending}
            onClick={() =>
              savePermissions.mutate(
                create(SetUserPermissionsRequestSchema, {
                  userId: user.id,
                  permissionSlugs: [...permissionPicked]
                })
              )
            }
          >
            {savePermissions.isPending ? <Spinner /> : null}
            Save permissions
          </Button>
        </Can>
      </CardContent>
    </Card>
  )
}

/**
 * The account's activity (D6): `ListForUser` as the detail's tab — one page
 * of the account's own records with the shared pager, the same rows the
 * console renders. The page state is the card's own; the card rides the
 * detail page and carries no URL of its own.
 */
function ActivityCard({ userId }: { userId: string }) {
  const [page, setPage] = useState(1)
  const logs = usePaginatedList(
    AuditLogService.method.listForUser,
    create(ListForUserRequestSchema, { userId, ...listPageInput(page) })
  )
  const rows = logs.data?.logs ?? []
  const summary = logs.summary

  return (
    <Card>
      <CardHeader>
        <CardTitle>Activity</CardTitle>
        <CardDescription>
          The actions the record keeps about this account, newest first.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {logs.isPending ? (
          <Spinner />
        ) : rows.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No activity yet</EmptyTitle>
              <EmptyDescription>The account has no recorded actions.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <AuditTable records={rows} />
        )}

        {summary && (summary.hasPrevious || summary.hasNext) ? (
          <ListPagination
            page={summary.page}
            totalPages={summary.totalPages}
            hasPrevious={summary.hasPrevious}
            hasNext={summary.hasNext}
            onPageChange={setPage}
          />
        ) : null}
      </CardContent>
    </Card>
  )
}
