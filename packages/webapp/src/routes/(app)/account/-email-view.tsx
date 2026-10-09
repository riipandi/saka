import { create } from '@bufbuild/protobuf'
import { Code, ConnectError } from '@connectrpc/connect'
import { createConnectQueryKey, useMutation, useQuery } from '@connectrpc/connect-query'
import { Mail, MailCheck } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Field, FieldDescription, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { OTPField, OTPFieldSlot } from 'uilibs/components/base/otp-field'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { useAuthUser } from '#/hooks/use-auth'
import { usePublicSettings } from '#/hooks/use-public-settings'
import { setAuthUser } from '#/libraries/guard/auth-store'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { styles } from '#/styles/pages/email.stylex'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  ConfirmEmailChangeRequestSchema,
  EmailVerificationService,
  GetCurrentUserRequestSchema,
  RequestEmailChangeRequestSchema,
  SendVerificationEmailRequestSchema,
  UserService,
  VerifyEmailRequestSchema
} from '~/codegen/identity_pb'

const KEY_CHANGE_EMAIL = 'users.change_email_enabled'

/** The length the backend's code generator draws (an unambiguous alphabet). */
const CODE_LENGTH = 12

/**
 * The code screen's single entry: twelve unambiguous characters, typed or
 * pasted whole. The slots exist so one character is one keypress — the
 * paste path fills them in one gesture.
 */
function CodeEntry({
  value,
  onValueChange,
  disabled
}: {
  value: string
  onValueChange: (next: string) => void
  disabled?: boolean
}) {
  return (
    <OTPField length={CODE_LENGTH} value={value} onValueChange={onValueChange} disabled={disabled}>
      <OTPFieldSlot />
      <OTPFieldSlot />
      <OTPFieldSlot />
      <OTPFieldSlot />
      <OTPFieldSlot />
      <OTPFieldSlot />
      <OTPFieldSlot />
      <OTPFieldSlot />
      <OTPFieldSlot />
      <OTPFieldSlot />
      <OTPFieldSlot />
      <OTPFieldSlot />
    </OTPField>
  )
}

/**
 * The already-verified refusal is the state arriving out of order, not a
 * failure: the badge is the truth, so the account refetches and the send
 * action disappears with it.
 */
function isAlreadyVerified(error: unknown): error is ConnectError {
  return error instanceof ConnectError && error.code === Code.FailedPrecondition
}

/**
 * The email identity page: the address on record and its verified state,
 * the verification code the message carries typed back, and the change
 * flow — request the new address, then confirm by the code the new
 * address receives — gated by the deployment's `users/change_email_enabled`.
 */
export function EmailView() {
  const queryClient = useQueryClient()
  const user = useAuthUser()
  const settings = usePublicSettings()
  const account = useQuery(UserService.method.getCurrentUser, create(GetCurrentUserRequestSchema))

  const userKey = createConnectQueryKey({
    schema: UserService.method.getCurrentUser,
    input: create(GetCurrentUserRequestSchema),
    cardinality: 'finite'
  })

  const [verifyCode, setVerifyCode] = useState('')
  const [verifyError, setVerifyError] = useState<string | null>(null)
  const [sendNote, setSendNote] = useState<string | null>(null)

  const [changeEmail, setChangeEmail] = useState('')
  const [changeError, setChangeError] = useState<string | null>(null)
  const [pendingEmail, setPendingEmail] = useState<string | null>(null)
  const [confirmCode, setConfirmCode] = useState('')
  const [confirmError, setConfirmError] = useState<string | null>(null)
  const [changedNote, setChangedNote] = useState<string | null>(null)

  const send = useMutation(EmailVerificationService.method.sendEmail, {
    onSuccess: () => {
      setSendNote('The verification code was sent. It is valid for one hour and works once.')
    },
    onError: (error) => {
      if (isAlreadyVerified(error)) {
        void queryClient.invalidateQueries({ queryKey: userKey })
        return
      }
      setSendNote(getErrorMessage(error))
    }
  })

  const verify = useMutation(EmailVerificationService.method.verifyEmail, {
    onSuccess: () => {
      setVerifyError(null)
      setVerifyCode('')
      void queryClient.invalidateQueries({ queryKey: userKey })
    },
    onError: (error) => setVerifyError(getErrorMessage(error))
  })

  const requestChange = useMutation(EmailVerificationService.method.requestEmailChange, {
    onSuccess: (_response, input) => {
      setChangeError(null)
      setPendingEmail(input.newEmail ?? '')
      setConfirmCode('')
    },
    onError: (error) => setChangeError(getErrorMessage(error))
  })

  const confirmChange = useMutation(EmailVerificationService.method.confirmEmailChange, {
    onSuccess: () => {
      // The confirmation is answered without a caller, so the account's new
      // address arrives only through the refetch; the store's copy is
      // patched now so the shell's greeting does not lag a page behind.
      if (pendingEmail && user) {
        setAuthUser({ ...user, email: pendingEmail })
      }
      setConfirmError(null)
      setPendingEmail(null)
      setConfirmCode('')
      setChangeEmail('')
      setChangedNote('The email address was changed.')
      void queryClient.invalidateQueries({ queryKey: userKey })
    },
    onError: (error) => setConfirmError(getErrorMessage(error))
  })

  const current = account.data?.user
  const verified = current?.emailVerified ?? false
  const changeEnabled = settings.isOn(KEY_CHANGE_EMAIL)

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
            Email
          </Text>
          <Text variant='body-2' color='neutral-faded'>
            The address this account answers to, its proof, and its changes.
          </Text>
        </div>
      </div>

      <div {...stylex.props(pageStyles.stack)}>
        <Card>
          <CardHeader>
            <CardTitle>Address on record</CardTitle>
            <CardDescription>Messages the account needs are sent here.</CardDescription>
            <CardAction>
              {verified ? (
                <Badge variant='secondary'>Verified</Badge>
              ) : (
                <Badge variant='outline'>Not verified</Badge>
              )}
            </CardAction>
          </CardHeader>
          <CardContent>
            {account.isPending ? (
              <Spinner />
            ) : (
              <div {...stylex.props(styles.addressRow)}>
                <Text render={<p />} variant='body-1' weight='medium'>
                  {current?.email ?? user?.email ?? '—'}
                </Text>
                {!verified ? (
                  <Button
                    variant='outline'
                    size='sm'
                    disabled={send.isPending}
                    onClick={() => {
                      setSendNote(null)
                      send.mutate(create(SendVerificationEmailRequestSchema))
                    }}
                  >
                    {send.isPending ? <Spinner /> : <Mail size={16} />}
                    Send verification code
                  </Button>
                ) : null}
              </div>
            )}
            {sendNote ? (
              <Text render={<p />} variant='body-2' color='neutral-faded' style={styles.note}>
                {sendNote}
              </Text>
            ) : null}
          </CardContent>
        </Card>

        {!verified ? (
          <Card>
            <CardHeader>
              <CardTitle>Verify this address</CardTitle>
              <CardDescription>
                Type the code the message carries. It works once, then it is spent.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <div {...stylex.props(styles.codeRow)}>
                <CodeEntry value={verifyCode} onValueChange={setVerifyCode} />
                <Button
                  disabled={verifyCode.trim().length !== CODE_LENGTH || verify.isPending}
                  onClick={() => {
                    verify.mutate(create(VerifyEmailRequestSchema, { token: verifyCode.trim() }))
                  }}
                >
                  {verify.isPending ? <Spinner /> : <MailCheck size={16} />}
                  Verify
                </Button>
              </div>
              {verifyError ? (
                <Text render={<p />} variant='body-2' color='critical' style={styles.note}>
                  {verifyError}
                </Text>
              ) : null}
            </CardContent>
          </Card>
        ) : null}

        <Card>
          <CardHeader>
            <CardTitle>Change address</CardTitle>
            <CardDescription>
              The new address receives the confirm code; the account moves only when it is typed
              back.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {changedNote ? (
              <Text render={<p />} variant='body-2' color='neutral-faded' style={styles.note}>
                {changedNote}
              </Text>
            ) : null}
            {pendingEmail ? (
              <div {...stylex.props(styles.codeRow)}>
                <div {...stylex.props(styles.grow)}>
                  <Text variant='body-2' color='neutral-faded'>
                    The confirm code was sent to {pendingEmail}.
                  </Text>
                  <CodeEntry
                    value={confirmCode}
                    onValueChange={setConfirmCode}
                    disabled={confirmChange.isPending}
                  />
                </div>
                <Button
                  variant='outline'
                  disabled={confirmCode.trim().length !== CODE_LENGTH || confirmChange.isPending}
                  onClick={() => {
                    confirmChange.mutate(
                      create(ConfirmEmailChangeRequestSchema, { token: confirmCode.trim() })
                    )
                  }}
                >
                  {confirmChange.isPending ? <Spinner /> : <MailCheck size={16} />}
                  Confirm change
                </Button>
              </div>
            ) : (
              <form
                onSubmit={(event) => {
                  event.preventDefault()
                  requestChange.mutate(
                    create(RequestEmailChangeRequestSchema, { newEmail: changeEmail.trim() })
                  )
                }}
              >
                <div {...stylex.props(styles.addressRow)}>
                  <Field style={styles.grow}>
                    <FieldLabel htmlFor='new-email'>New address</FieldLabel>
                    <Input
                      id='new-email'
                      type='email'
                      value={changeEmail}
                      placeholder='you@example.com'
                      autoComplete='email'
                      disabled={!changeEnabled || requestChange.isPending}
                      onChange={(event) => setChangeEmail(event.target.value)}
                    />
                    {!changeEnabled ? (
                      <FieldDescription>
                        This deployment does not allow email changes.
                      </FieldDescription>
                    ) : null}
                  </Field>
                  <Button
                    type='submit'
                    variant='outline'
                    disabled={
                      !changeEnabled || changeEmail.trim().length === 0 || requestChange.isPending
                    }
                  >
                    {requestChange.isPending ? <Spinner /> : <Mail size={16} />}
                    Request change
                  </Button>
                </div>
              </form>
            )}
            {changeError ? (
              <Text render={<p />} variant='body-2' color='critical' style={styles.note}>
                {changeError}
              </Text>
            ) : null}
            {confirmError ? (
              <Text render={<p />} variant='body-2' color='critical' style={styles.note}>
                {confirmError}
              </Text>
            ) : null}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
