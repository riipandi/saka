import { useTransport } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useForm } from '@tanstack/react-form'
import { useEffect, useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Field, FieldLabel } from 'uilibs/components/base/field'
import { Alert, AlertDescription, AlertTitle } from 'uilibs/components/extra/alert'
import { Card, CardContent, CardDescription, CardHeader } from 'uilibs/components/extra/card'
import { LoaderText } from 'uilibs/components/extra/loader-text'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { useAuthentication } from '#/hooks/use-auth'
import type { SignInChallenge } from '#/libraries/guard/auth-engine'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { runPasskeyAssertion } from '#/libraries/webauthn/passkey'
import { styles } from '#/styles/pages/login.stylex'

/** Seconds the bridge countdown shows between renders. */
const COUNTDOWN_TICK_MS = 1000

/** The seconds left before the bridge dies, ticked once a second. Shared by
 * the challenge and the enrollment views — both bridges die the same way. */
export function useBridgeCountdown(expiresAt: number): number {
  const [secondsLeft, setSecondsLeft] = useState(() =>
    Math.max(0, Math.floor((expiresAt - Date.now()) / 1000))
  )
  useEffect(() => {
    const tick = () => setSecondsLeft(Math.max(0, Math.floor((expiresAt - Date.now()) / 1000)))
    const timer = setInterval(tick, COUNTDOWN_TICK_MS)
    return () => clearInterval(timer)
  }, [expiresAt])
  return secondsLeft
}

/**
 * The challenge phase: the bridge the password check minted is spent with
 * the second factor — the authenticator's current code or a recovery code,
 * both arriving as the wire's `code` factor. The countdown is the wire's
 * expiry, and a dead bridge restarts the sign-in: no refresh, no second
 * chance, the password check would have to answer again anyway.
 */
export function MfaChallengeView({
  challenge,
  returnTo,
  onRestart
}: {
  challenge: SignInChallenge
  returnTo?: string
  onRestart: () => void
}) {
  const { completeSignIn } = useAuthentication()
  const transport = useTransport()
  const [failed, setFailed] = useState<string | null>(null)
  const [passkeyBusy, setPasskeyBusy] = useState(false)
  const secondsLeft = useBridgeCountdown(challenge.expiresAt)
  const expired = secondsLeft <= 0

  const proveWithPasskey = async () => {
    setFailed(null)
    setPasskeyBusy(true)
    try {
      // The same ceremony every passkey proof rides — the account here is
      // the bridge's, the credential must answer to it.
      const assertion = await runPasskeyAssertion(transport)
      await completeSignIn(
        challenge.pendingToken,
        { kind: 'passkey', sessionId: assertion.sessionId, credential: assertion.credential },
        { redirectTo: returnTo }
      )
    } catch (error: unknown) {
      setFailed(getErrorMessage(error))
    } finally {
      setPasskeyBusy(false)
    }
  }

  const form = useForm({
    defaultValues: { code: '' },
    onSubmit: async ({ value }) => {
      setFailed(null)
      try {
        await completeSignIn(
          challenge.pendingToken,
          { kind: 'code', code: value.code },
          { redirectTo: returnTo }
        )
        // The provider navigated on success; the bridge is spent.
      } catch (error: unknown) {
        setFailed(getErrorMessage(error))
      }
    }
  })

  return (
    <div {...stylex.props(styles.page)}>
      <Card size='md' id='mfa-challenge-card' style={styles.cardRoot}>
        <CardHeader style={styles.header}>
          <Text render={<h1 />} variant='featured-5' weight='semibold'>
            Two-factor verification
          </Text>
          <CardDescription>
            Enter the code from your authenticator app, one of your recovery codes, or prove with a
            passkey.
          </CardDescription>
        </CardHeader>

        <CardContent>
          {expired ? (
            <>
              <Alert variant='destructive' id='mfa-challenge-expired'>
                <AlertTitle>The verification window closed</AlertTitle>
                <AlertDescription>
                  The sign-in attempt expired. Sign in again to continue.
                </AlertDescription>
              </Alert>
              <Button variant='outline' onClick={onRestart} style={styles.submit}>
                Back to sign in
              </Button>
            </>
          ) : (
            <>
              <Text variant='body-2' color='neutral-faded' style={styles.countdown}>
                The verification expires in {secondsLeft}s.
              </Text>
              <Button
                type='button'
                variant='outline'
                disabled={passkeyBusy}
                onClick={() => void proveWithPasskey()}
                style={styles.submit}
              >
                {passkeyBusy && <Spinner />}
                {passkeyBusy ? 'Waiting for your authenticator…' : 'Use a passkey instead'}
              </Button>
              <form
                id='mfa-challenge-form'
                autoComplete='one-time-code'
                onSubmit={(e) => {
                  e.preventDefault()
                  e.stopPropagation()
                  void form.handleSubmit()
                }}
              >
                <form.Field
                  name='code'
                  validators={{
                    onChange: ({ value }) =>
                      /^[0-9]{6,8}$/.test(value) || value.length === 0
                        ? undefined
                        : { message: 'Enter the 6 to 8 digit code' }
                  }}
                  children={(field) => {
                    const error = field.state.meta.errors?.[0]?.message
                    return (
                      <Field id='field-mfa-code' invalid={!!error}>
                        <FieldLabel htmlFor='mfa-code'>Verification code</FieldLabel>
                        <input
                          id='mfa-code'
                          name='code'
                          inputMode='numeric'
                          autoComplete='one-time-code'
                          autoFocus
                          {...stylex.props(styles.mfaCodeInput)}
                          value={field.state.value}
                          onChange={(e) => {
                            setFailed(null)
                            field.handleChange(e.target.value.replace(/\D/g, '').slice(0, 8))
                          }}
                          onBlur={field.handleBlur}
                        />
                      </Field>
                    )
                  }}
                />
                {failed ? (
                  <Alert variant='destructive' id='mfa-challenge-error'>
                    <AlertTitle>Verification failed</AlertTitle>
                    <AlertDescription>{failed}</AlertDescription>
                  </Alert>
                ) : null}
                <form.Subscribe
                  selector={(state) => [state.canSubmit, state.isSubmitting] as const}
                  children={([canSubmit, isSubmitting]) => (
                    <Button
                      type='submit'
                      variant='primary'
                      disabled={!canSubmit || expired}
                      style={styles.submit}
                    >
                      {isSubmitting && <Spinner />}
                      {isSubmitting ? (
                        <LoaderText variant='body-2'>Verifying…</LoaderText>
                      ) : (
                        'Verify'
                      )}
                    </Button>
                  )}
                />
              </form>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
