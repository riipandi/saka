import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Bin, ImagePlus, Power, RotateCcw } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
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
import { Field, FieldDescription, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { LoaderText } from 'uilibs/components/extra/loader-text'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { ThemeSwitcher } from 'uilibs/theme'
import { UserAvatar } from '#/components/user-avatar'
import { useAuthentication, useAuthUser } from '#/hooks/use-auth'
import { usePublicSettings } from '#/hooks/use-public-settings'
import { fetcher } from '#/libraries/api-client'
import { setAuthUser } from '#/libraries/guard/auth-store'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import { styles } from '#/styles/pages/settings.stylex'
import {
  GetCurrentUserRequestSchema,
  UpdateCurrentUserRequestSchema,
  DeleteMyAccountRequestSchema,
  type User,
  UserService
} from '~/codegen/identity_pb'

/** The public settings keys this page gates its actions by. */
const KEY_CHANGE_USERNAME = 'users.change_username_enabled'
const KEY_SELF_DELETE = 'users.self_delete_enabled'

/** The largest picture the backend accepts — the client pre-check is UX, the byte cap is the server's. */
const MAX_PICTURE_BYTES = 2 * 1024 * 1024
/** The kinds the backend's magic-byte sniff accepts. */
const PICTURE_KINDS = ['image/png', 'image/jpeg', 'image/webp']

/** The profile fields the form edits; strings, because inputs hold strings. */
interface ProfileDraft {
  firstName: string
  lastName: string
  displayName: string
  locale: string
  timezone: string
  username: string
}

function draftFrom(user: User): ProfileDraft {
  return {
    firstName: user.firstName ?? '',
    lastName: user.lastName ?? '',
    displayName: user.displayName,
    locale: user.metadata?.locale ?? '',
    timezone: user.metadata?.timezone ?? '',
    username: user.username
  }
}

export const Route = createFileRoute('/(app)/settings')({
  component: RouteComponent,
  staticData: {
    pageTitle: 'Settings'
  }
})

export function RouteComponent() {
  const storeUser = useAuthUser()
  const { logout } = useAuthentication()
  const queryClient = useQueryClient()
  const settings = usePublicSettings()

  const account = useQuery(UserService.method.getCurrentUser, create(GetCurrentUserRequestSchema))

  // The form holds a draft the account view seeds — the response is the
  // authority (D2 of the wave plan), and every refetch re-seeds it.
  const [draft, setDraft] = useState<ProfileDraft | null>(null)
  useEffect(() => {
    if (account.data?.user) setDraft(draftFrom(account.data.user))
  }, [account.data])

  const [formError, setFormError] = useState<string | null>(null)
  const [pictureError, setPictureError] = useState<string | null>(null)
  const [pictureBusy, setPictureBusy] = useState<'upload' | 'reset' | null>(null)
  // The picture URL is stable while its content changes, so the view busts
  // the browser cache with its own epoch — bumped on every picture write.
  const [pictureEpoch, setPictureEpoch] = useState(0)

  const user = account.data?.user
  const pictureUrl =
    user?.picture && pictureEpoch > 0
      ? `${user.picture}${user.picture.includes('?') ? '&' : '?'}v=${pictureEpoch}`
      : user?.picture

  const userKey = createConnectQueryKey({
    schema: UserService.method.getCurrentUser,
    input: create(GetCurrentUserRequestSchema),
    cardinality: 'finite'
  })

  const update = useMutation(UserService.method.updateCurrentUser, {
    onSuccess: (response) => {
      setFormError(null)
      // The answered view is the authority: the store's profile is patched
      // from it (the claims stay stale until the next mint — cosmetic only),
      // and the draft re-seeds from the same response.
      if (response.user) {
        setAuthUser({
          id: response.user.id,
          username: response.user.username,
          email: response.user.email,
          displayName: response.user.displayName
        })
        setDraft(draftFrom(response.user))
      }
      void queryClient.invalidateQueries({ queryKey: userKey })
    }
  })

  const removeAccount = useMutation(UserService.method.deleteMyAccount, {
    onSuccess: () => {
      // The account is gone server-side; the pair is dropped client-side
      // (a failing SignOut is tolerated by the engine) and the shell's
      // eviction effect carries the browser to the sign-in page.
      logout()
    }
  })

  const usernameEditable = settings.isOn(KEY_CHANGE_USERNAME)
  const selfDeleteEnabled = settings.isOn(KEY_SELF_DELETE)

  const dirty = !!user && !!draft && JSON.stringify(draftFrom(user)) !== JSON.stringify(draft)

  const saveProfile = () => {
    if (!draft) return
    if (!draft.firstName.trim() || !draft.lastName.trim() || !draft.displayName.trim()) {
      setFormError('First name, last name, and display name are required.')
      return
    }
    setFormError(null)
    update.mutate(
      create(UpdateCurrentUserRequestSchema, {
        firstName: draft.firstName.trim(),
        lastName: draft.lastName.trim(),
        displayName: draft.displayName.trim(),
        locale: draft.locale.trim(),
        timezone: draft.timezone.trim(),
        username: draft.username !== user?.username ? draft.username : undefined
      })
    )
  }

  const fileInput = useRef<HTMLInputElement>(null)

  const uploadPicture = async (file: File) => {
    setPictureError(null)
    if (!PICTURE_KINDS.includes(file.type)) {
      setPictureError('The picture must be a PNG, JPEG, or WebP image.')
      return
    }
    if (file.size > MAX_PICTURE_BYTES) {
      setPictureError('The picture must be at most 2 MiB.')
      return
    }
    setPictureBusy('upload')
    try {
      // The raw body is the picture — the REST contract takes the bytes,
      // not a multipart wrapper (the raw-body path of the fetch seam).
      await fetcher('users/me/profile-picture', { method: 'PUT', body: file })
      setPictureEpoch((epoch) => epoch + 1)
      await queryClient.invalidateQueries({ queryKey: userKey })
    } catch (error) {
      setPictureError(getErrorMessage(error))
    } finally {
      setPictureBusy(null)
    }
  }

  const resetPicture = async () => {
    setPictureError(null)
    setPictureBusy('reset')
    try {
      await fetcher('users/me/profile-picture', { method: 'DELETE' })
      setPictureEpoch((epoch) => epoch + 1)
      await queryClient.invalidateQueries({ queryKey: userKey })
    } catch (error) {
      setPictureError(getErrorMessage(error))
    } finally {
      setPictureBusy(null)
    }
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
            Account
          </Text>
          <Text render={<h1 />} variant='featured-4' weight='bold'>
            Settings
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            Adjust your workspace preferences.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Profile picture</CardTitle>
            <CardDescription>A square picture under 2 MiB — PNG, JPEG, or WebP.</CardDescription>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(styles.pictureRow)}>
              <UserAvatar
                name={storeUser?.displayName || storeUser?.username || 'Guest'}
                picture={pictureUrl}
                size='lg'
              />
              <div {...stylex.props(styles.pictureActions)}>
                <input
                  ref={fileInput}
                  type='file'
                  accept={PICTURE_KINDS.join(',')}
                  hidden
                  onChange={(event) => {
                    const file = event.target.files?.[0]
                    if (file) void uploadPicture(file)
                    event.target.value = ''
                  }}
                />
                <Button
                  variant='outline'
                  size='sm'
                  disabled={pictureBusy !== null}
                  onClick={() => fileInput.current?.click()}
                >
                  {pictureBusy === 'upload' ? <Spinner /> : <ImagePlus size={16} />}
                  Upload
                </Button>
                {user?.picture ? (
                  <Button
                    variant='ghost'
                    size='sm'
                    disabled={pictureBusy !== null}
                    onClick={() => void resetPicture()}
                  >
                    {pictureBusy === 'reset' ? <Spinner /> : <RotateCcw size={16} />}
                    Reset
                  </Button>
                ) : null}
              </div>
            </div>
            {pictureError ? (
              <Text render={<p />} variant='body-2' color='critical'>
                {pictureError}
              </Text>
            ) : null}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Profile</CardTitle>
            <CardDescription>The names and the preferences the account carries.</CardDescription>
            <CardAction>
              <Button
                variant='primary'
                size='sm'
                disabled={!dirty || update.isPending}
                onClick={saveProfile}
              >
                {update.isPending ? <Spinner /> : null}
                {update.isPending ? (
                  <LoaderText variant='body-2'>Saving…</LoaderText>
                ) : (
                  'Save changes'
                )}
              </Button>
            </CardAction>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(styles.profileRow)}>
              <Field style={styles.grow}>
                <FieldLabel htmlFor='first-name'>First name</FieldLabel>
                <Input
                  id='first-name'
                  value={draft?.firstName ?? ''}
                  onChange={(event) =>
                    setDraft((d) => (d ? { ...d, firstName: event.target.value } : d))
                  }
                />
              </Field>
              <Field style={styles.grow}>
                <FieldLabel htmlFor='last-name'>Last name</FieldLabel>
                <Input
                  id='last-name'
                  value={draft?.lastName ?? ''}
                  onChange={(event) =>
                    setDraft((d) => (d ? { ...d, lastName: event.target.value } : d))
                  }
                />
              </Field>
            </div>
            <div {...stylex.props(styles.profileRow)}>
              <Field style={styles.grow}>
                <FieldLabel htmlFor='display-name'>Display name</FieldLabel>
                <Input
                  id='display-name'
                  value={draft?.displayName ?? ''}
                  onChange={(event) =>
                    setDraft((d) => (d ? { ...d, displayName: event.target.value } : d))
                  }
                />
              </Field>
              <Field style={styles.grow}>
                <FieldLabel htmlFor='username'>Username</FieldLabel>
                <Input
                  id='username'
                  value={draft?.username ?? ''}
                  disabled={!usernameEditable}
                  onChange={(event) =>
                    setDraft((d) => (d ? { ...d, username: event.target.value } : d))
                  }
                />
                {!usernameEditable ? (
                  <FieldDescription>
                    This deployment does not allow username changes.
                  </FieldDescription>
                ) : null}
              </Field>
            </div>
            <div {...stylex.props(styles.profileRow)}>
              <Field style={styles.grow}>
                <FieldLabel htmlFor='email'>Email</FieldLabel>
                <Input id='email' value={user?.email ?? ''} readOnly />
              </Field>
              <Field style={styles.grow}>
                <FieldLabel htmlFor='locale'>Locale</FieldLabel>
                <Input
                  id='locale'
                  placeholder='e.g. en or id'
                  value={draft?.locale ?? ''}
                  onChange={(event) =>
                    setDraft((d) => (d ? { ...d, locale: event.target.value } : d))
                  }
                />
                <FieldDescription>A preferred locale tag; empty clears it.</FieldDescription>
              </Field>
              <Field style={styles.grow}>
                <FieldLabel htmlFor='timezone'>Timezone</FieldLabel>
                <Input
                  id='timezone'
                  placeholder='e.g. Asia/Jakarta'
                  value={draft?.timezone ?? ''}
                  onChange={(event) =>
                    setDraft((d) => (d ? { ...d, timezone: event.target.value } : d))
                  }
                />
                <FieldDescription>An IANA zone; empty means UTC.</FieldDescription>
              </Field>
            </div>
            {formError || update.error ? (
              <Text render={<p />} variant='body-2' color='critical'>
                {formError ?? getErrorMessage(update.error)}
              </Text>
            ) : null}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Appearance</CardTitle>
            <CardDescription>Choose how the interface looks on this device.</CardDescription>
            <CardAction>
              <ThemeSwitcher />
            </CardAction>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(styles.appearanceRow)}>
              <Text render={<p />} variant='body-1' weight='semibold'>
                Color theme
              </Text>
              <Text variant='body-2' color='neutral-faded'>
                Applies instantly and persists across reloads.
              </Text>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Danger zone</CardTitle>
            <CardDescription>Irreversible actions for the current session.</CardDescription>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(styles.dangerRow)}>
              <div {...stylex.props(styles.dangerCopy)}>
                <Text render={<p />} variant='body-1' weight='semibold'>
                  Sign out
                </Text>
                <Text variant='body-2' color='neutral-faded'>
                  Terminate the current session and return to the sign-in page.
                </Text>
              </div>
              <Button variant='destructive' onClick={() => logout()}>
                <Power size={16} />
                Sign out
              </Button>
            </div>

            {selfDeleteEnabled ? (
              <div {...stylex.props(styles.dangerRow)}>
                <div {...stylex.props(styles.dangerCopy)}>
                  <Text render={<p />} variant='body-1' weight='semibold'>
                    Delete this account
                  </Text>
                  <Text variant='body-2' color='neutral-faded'>
                    Removes the account and ends its sessions. This cannot be undone.
                  </Text>
                </div>
                <DeleteAccountDialog
                  username={user?.username ?? ''}
                  onDelete={() => removeAccount.mutate(create(DeleteMyAccountRequestSchema))}
                  pending={removeAccount.isPending}
                  error={removeAccount.error ? getErrorMessage(removeAccount.error) : null}
                />
              </div>
            ) : null}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

/**
 * The typed confirmation the deletion demands: the dialog owns its open
 * state and the typed word, and the confirm button stays disabled until the
 * typed value matches — the account's handle is the one word the deleter
 * cannot mean by accident.
 */
function DeleteAccountDialog({
  username,
  onDelete,
  pending,
  error
}: {
  username: string
  onDelete: () => void
  pending: boolean
  error: string | null
}) {
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  return (
    <AlertDialog
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen)
        if (!nextOpen) setTyped('')
      }}
    >
      <AlertDialogTrigger render={<Button variant='destructive' />}>
        <Bin size={16} />
        Delete account
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete this account?</AlertDialogTitle>
          <AlertDialogDescription>
            This removes the account permanently — its sessions end and its profile picture is
            discarded. Type the username to confirm.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Field>
          <FieldLabel htmlFor='delete-account-confirm'>Username</FieldLabel>
          <Input
            id='delete-account-confirm'
            value={typed}
            placeholder={username}
            autoComplete='off'
            onChange={(event) => setTyped(event.target.value)}
          />
          {error ? (
            <Text render={<p />} variant='body-2' color='critical'>
              {error}
            </Text>
          ) : null}
        </Field>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant='destructive'
            disabled={typed !== username || pending}
            onClick={() => onDelete()}
          >
            {pending ? <Spinner /> : <Bin size={16} />}
            Delete account
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
