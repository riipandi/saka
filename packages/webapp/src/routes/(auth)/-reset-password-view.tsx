import { useMutation } from '@connectrpc/connect-query'
import atoms from '@stylexjs/atoms'
import * as stylex from '@stylexjs/stylex'
import { Link, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Field, FieldDescription, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { Alert, AlertDescription, AlertTitle } from 'uilibs/components/extra/alert'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader
} from 'uilibs/components/extra/card'
import { InputPassword } from 'uilibs/components/extra/input-password'
import { LoaderText } from 'uilibs/components/extra/loader-text'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { styles } from '#/styles/pages/login.stylex'
import { PasswordRecoveryService } from '~/codegen/authn_pb'

/** The floor the reset form asks before the backend judges the policy — the
 * deployment's settings own the real bound, this is the hint. */
const PASSWORD_HINT_MIN = 8

/**
 * The reset screen: the emailed code is the whole credential — the caller
 * carries no session, the same way VerifyEmail does. The search param is
 * the prefill (a deployment whose email links here lands with it set); the
 * field is the primary path, because the email's own shape is a code to
 * type. Success revokes every live session and hands the caller back to
 * the sign-in.
 */
export function ResetPasswordView({ prefill }: { prefill?: string }) {
  const navigate = useNavigate()
  const [code, setCode] = useState(prefill ?? '')
  const [newPassword, setNewPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [failed, setFailed] = useState<string | null>(null)

  const resetPassword = useMutation(PasswordRecoveryService.method.resetPassword, {
    onSuccess: () => {
      void navigate({ to: '/login', search: { reset: true } })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const mismatch = confirm.length > 0 && confirm !== newPassword
  const ready =
    code.length > 0 && newPassword.length >= PASSWORD_HINT_MIN && confirm === newPassword

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    setFailed(null)
    // terminate_sessions rides its default: a reset means the old credential
    // is no longer trusted, and the caller who reset from another session
    // is signed out with everyone else.
    resetPassword.mutate({ token: code, newPassword })
  }

  return (
    <Card size='md' id='reset-password-card' style={styles.cardRoot}>
      <CardHeader style={styles.header}>
        <Text render={<h1 />} variant='featured-5' weight='semibold'>
          Reset your password
        </Text>
        <CardDescription>
          Enter the code the email carried, then choose the credential this account answers with
          from now on.
        </CardDescription>
      </CardHeader>

      <CardContent>
        <form id='reset-password-form' autoComplete='on' onSubmit={submit}>
          {failed ? (
            <Alert variant='destructive' id='reset-password-error'>
              <AlertTitle>Reset failed</AlertTitle>
              <AlertDescription>{failed}</AlertDescription>
            </Alert>
          ) : null}
          <Field id='field-reset-code'>
            <FieldLabel htmlFor='reset-code'>Reset code</FieldLabel>
            <Input
              id='reset-code'
              name='token'
              value={code}
              onChange={(e) => setCode(e.target.value)}
              autoComplete='off'
              required
              autoFocus={!prefill}
            />
            <FieldDescription>The 64-character code the email carried.</FieldDescription>
          </Field>
          <Field id='field-reset-password'>
            <FieldLabel htmlFor='reset-password'>New password</FieldLabel>
            <InputPassword
              id='reset-password'
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              autoComplete='new-password'
            />
            <FieldDescription>
              At least {PASSWORD_HINT_MIN} characters — the deployment's policy has the final word.
            </FieldDescription>
          </Field>
          <Field id='field-reset-password-confirm' invalid={mismatch}>
            <FieldLabel htmlFor='reset-password-confirm'>Confirm it</FieldLabel>
            <InputPassword
              id='reset-password-confirm'
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              autoComplete='new-password'
            />
          </Field>
          <div {...stylex.props(styles.submitWrapper)}>
            <Button
              type='submit'
              variant='primary'
              disabled={!ready || resetPassword.isPending}
              style={styles.submit}
            >
              {resetPassword.isPending && <Spinner />}
              {resetPassword.isPending ? (
                <LoaderText variant='body-2'>Resetting…</LoaderText>
              ) : (
                'Reset password'
              )}
            </Button>
          </div>
        </form>
      </CardContent>

      <CardFooter style={atoms.justifyContent.center}>
        <Text variant='body-2' color='neutral-faded'>
          Remembered it?{' '}
          <Link to='/login' {...stylex.props(styles.backLink)}>
            Back to sign in
          </Link>
        </Text>
      </CardFooter>
    </Card>
  )
}
