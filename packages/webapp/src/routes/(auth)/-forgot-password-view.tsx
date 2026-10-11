import { useMutation } from '@connectrpc/connect-query'
import atoms from '@stylexjs/atoms'
import * as stylex from '@stylexjs/stylex'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Field, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { Alert, AlertDescription, AlertTitle } from 'uilibs/components/extra/alert'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader
} from 'uilibs/components/extra/card'
import { LoaderText } from 'uilibs/components/extra/loader-text'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { styles } from '#/styles/pages/login.stylex'
import { PasswordRecoveryService } from '~/codegen/authn_pb'

const devStyles = stylex.create({
  token: {
    fontFamily: 'monospace',
    wordBreak: 'break-all'
  }
})

/**
 * The forgot-password form. The answer is success whether the address
 * names an account or not — the page says the same sentence either way,
 * because the endpoint is not an account enumerator. A deployment running
 * the `expose_reset_token` development aid sees the raw token here, with a
 * link that carries it to the reset screen; every real deployment answers
 * the sentence alone and the email is the only carrier.
 */
export function ForgotPasswordView() {
  const [email, setEmail] = useState('')
  const [failed, setFailed] = useState<string | null>(null)
  const [sent, setSent] = useState<{ message: string; resetToken?: string } | null>(null)

  const forgotPassword = useMutation(PasswordRecoveryService.method.forgotPassword, {
    onSuccess: (response) => {
      setSent({ message: response.message, resetToken: response.resetToken || undefined })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    setFailed(null)
    forgotPassword.mutate({ email })
  }

  return (
    <Card size='md' id='forgot-password-card' style={styles.cardRoot}>
      <CardHeader style={styles.header}>
        <Text render={<h1 />} variant='featured-5' weight='semibold'>
          Forgot password?
        </Text>
        {sent ? (
          <CardDescription>{sent.message}</CardDescription>
        ) : (
          <CardDescription>
            Enter your account email and we will send you a reset code.
          </CardDescription>
        )}
      </CardHeader>

      <CardContent>
        {sent ? (
          <>
            <Alert id='forgot-password-sent'>
              <AlertTitle>Check your inbox</AlertTitle>
              <AlertDescription>
                The reset code travels by email and works once. It expires in an hour.
              </AlertDescription>
            </Alert>
            {sent.resetToken ? (
              <Alert id='forgot-password-dev-token'>
                <AlertTitle>Development mode</AlertTitle>
                <AlertDescription>
                  This deployment exposes the reset token for tooling:
                  <Text variant='body-2' style={devStyles.token}>
                    {sent.resetToken}
                  </Text>
                  <Link
                    to='/reset-password'
                    search={{ token: sent.resetToken }}
                    {...stylex.props(styles.backLink)}
                  >
                    Continue to the reset screen
                  </Link>
                </AlertDescription>
              </Alert>
            ) : null}
          </>
        ) : (
          <form id='forgot-password-form' autoComplete='on' onSubmit={submit}>
            {failed ? (
              <Alert variant='destructive' id='forgot-password-error'>
                <AlertTitle>Request failed</AlertTitle>
                <AlertDescription>{failed}</AlertDescription>
              </Alert>
            ) : null}
            <Field id='field-email'>
              <FieldLabel htmlFor='email'>Email</FieldLabel>
              <Input
                id='email'
                name='email'
                type='email'
                placeholder='you@example.com'
                autoComplete='email'
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </Field>
            <div {...stylex.props(styles.submitWrapper)}>
              <Button
                type='submit'
                variant='primary'
                disabled={forgotPassword.isPending}
                style={styles.submit}
              >
                {forgotPassword.isPending && <Spinner />}
                {forgotPassword.isPending ? (
                  <LoaderText variant='body-2'>Sending…</LoaderText>
                ) : (
                  'Send reset code'
                )}
              </Button>
            </div>
          </form>
        )}
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
