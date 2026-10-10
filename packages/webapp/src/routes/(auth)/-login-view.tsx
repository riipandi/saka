import { useTransport } from '@connectrpc/connect-query'
import atoms from '@stylexjs/atoms'
import * as stylex from '@stylexjs/stylex'
import { useForm } from '@tanstack/react-form'
import { Link, useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import type { ComponentType } from 'react'
import { ViewTransition } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Checkbox } from 'uilibs/components/base/checkbox'
import { Field, FieldError, FieldLabel, FieldSeparator } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { Alert, AlertDescription, AlertTitle } from 'uilibs/components/extra/alert'
import { ButtonGroup } from 'uilibs/components/extra/button-group'
import { CardFooter, CardHeader } from 'uilibs/components/extra/card'
import { Card, CardContent, CardDescription } from 'uilibs/components/extra/card'
import { InputPassword } from 'uilibs/components/extra/input-password'
import { LoaderText } from 'uilibs/components/extra/loader-text'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { GitHubIcon, GoogleIcon, ViteIcon } from 'uilibs/components/icons'
import { useAppConfig } from '#/hooks/use-app-config'
import { useAuthentication } from '#/hooks/use-auth'
import { useOAuthProviders } from '#/hooks/use-oauth-providers'
import type { SignInChallenge } from '#/libraries/guard/auth-engine'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { takeSignInChallenge } from '#/libraries/guard/sign-in-handoff'
import { runPasskeyAssertion } from '#/libraries/webauthn/passkey'
import { socialStyles, styles } from '#/styles/pages/login.stylex'
import { MfaChallengeView } from './-mfa-challenge-view'
import { MfaEnrollmentView } from './-mfa-enrollment-view'

/**
 * The code the one-time email carries — six or twelve characters per the
 * wire's validation, and uppercase: the emails render it that way.
 */
const ONE_TIME_CODE_PATTERN = /^[A-Za-z0-9]{6,12}$/

/** The icon the two builtin providers render; a custom connection carries none. */
const PROVIDER_ICONS: Record<string, ComponentType<{ size: number }>> = {
  google: GoogleIcon,
  github: GitHubIcon
}

/**
 * The one-time code entry — the email phase's second half. The code is
 * typed, never linked (the wire reserved its redirect path away): the email
 * names no device, so the device token this entry spends lives in the
 * login mount's memory alone, and a code read from a mailbox alone cannot
 * sign anyone in.
 */
function OneTimeCodeEntry({
  notice,
  failed,
  onFailed,
  onDismissFailed,
  onSubmit,
  onRestart
}: {
  notice: string
  failed: string | null
  onFailed: (message: string | null) => void
  onDismissFailed: () => void
  onSubmit: (code: string) => void
  onRestart: () => void
}) {
  const [code, setCode] = useState('')
  const valid = ONE_TIME_CODE_PATTERN.test(code.trim())
  const busy = false

  return (
    <Card size='md' id='login-one-time-card' style={styles.cardRoot}>
      <CardHeader style={styles.header}>
        <div {...stylex.props(styles.logo)}>
          <ViteIcon size={28} />
        </div>
        <Text render={<h1 />} variant='featured-5' weight='semibold'>
          {notice}
        </Text>
        <CardDescription>
          Enter the one-time code we emailed you. It works once and expires in fifteen minutes.
        </CardDescription>
      </CardHeader>

      <CardContent>
        <form
          id='one-time-code-form'
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            if (valid) onSubmit(code.trim())
          }}
        >
          {failed ? (
            <Alert variant='destructive' id='one-time-code-error'>
              <AlertTitle>Sign in failed</AlertTitle>
              <AlertDescription>{failed}</AlertDescription>
            </Alert>
          ) : null}
          <Field id='field-one-time-code'>
            <FieldLabel htmlFor='one-time-code'>One-time code</FieldLabel>
            <Input
              id='one-time-code'
              name='one-time-code'
              placeholder='GRYFFINDOR'
              autoComplete='one-time-code'
              value={code}
              onChange={(e) => {
                onFailed(null)
                onDismissFailed()
                setCode(e.target.value)
              }}
            />
          </Field>
          <div {...stylex.props(styles.submitWrapper)}>
            <Button type='submit' variant='primary' disabled={!valid || busy} style={styles.submit}>
              Sign in
            </Button>
          </div>
        </form>
      </CardContent>

      <CardFooter style={atoms.justifyContent.center}>
        <Text variant='body-2' color='neutral-faded'>
          <Link to='/login' onClick={onRestart} replace {...stylex.props(styles.backLink)}>
            Use another sign-in method
          </Link>
        </Text>
      </CardFooter>
    </Card>
  )
}
/**
 * The login flow's phases. The credentials form, the multi-factor challenge,
 * and the forced enrollment are states of one machine in one mount — the
 * pending bridge travels between them in memory alone (plan D3), so a URL
 * never carries it and back navigation restarts the sign-in.
 */
type LoginPhase =
  | { name: 'credentials' }
  | { name: 'challenge'; challenge: SignInChallenge }
  | { name: 'enrollment'; challenge: SignInChallenge }
  | { name: 'one-time-code'; deviceToken?: string; notice: string }

export function LoginView({
  loggedOut,
  unauthenticated,
  reset,
  returnTo
}: {
  loggedOut?: boolean
  unauthenticated?: boolean
  reset?: boolean
  returnTo?: string
}) {
  const navigate = useNavigate()
  const { login, verifyPasskeyLogin, requestOneTimeAccess, exchangeOneTimeToken } =
    useAuthentication()
  const { data: config } = useAppConfig()
  const { data: providers } = useOAuthProviders()
  const transport = useTransport()
  const [failed, setFailed] = useState<string | null>(null)
  const [remember, setRemember] = useState(false)
  const [dismissed, setDismissed] = useState(false)
  const [phase, setPhase] = useState<LoginPhase>({ name: 'credentials' })
  const [passkeyBusy, setPasskeyBusy] = useState(false)
  const oneTimeEnabled = config?.auth.one_time_access_email_as_unauthenticated_enabled ?? false

  // Goodbye, sign-in-required, and password-reset notices belong to the
  // redirect that brought the visitor here; refresh page or back-navigation
  // never replays them.
  const [arrivedLoggedOut] = useState(loggedOut)
  const [arrivedUnauthenticated] = useState(unauthenticated)
  const [arrivedReset] = useState(reset)
  const showGoodbye = Boolean(arrivedLoggedOut) && !dismissed && !failed
  const showSignInPrompt = Boolean(arrivedUnauthenticated) && !dismissed && !failed
  const showReset = Boolean(arrivedReset) && !dismissed && !failed

  useEffect(() => {
    if (!loggedOut && !unauthenticated && !reset) return
    void navigate({ to: '/login', search: { return_to: returnTo }, replace: true })
  }, [loggedOut, unauthenticated, reset, returnTo, navigate])

  // A sign-in that forked on another route (the OAuth callback) handed its
  // bridge over before landing here; taking it consumes the slot.
  useEffect(() => {
    const handed = takeSignInChallenge()
    if (!handed) return
    if (handed.kind === 'mfa-challenge') {
      setPhase({ name: 'challenge', challenge: handed })
    } else {
      setPhase({ name: 'enrollment', challenge: handed })
    }
  }, [])

  const clearAlerts = () => {
    setFailed(null)
    setDismissed(true)
  }

  const form = useForm({
    defaultValues: { identity: '', password: '' },
    onSubmit: async ({ value }) => {
      setFailed(null)
      try {
        const outcome = await login(value, { rememberMe: remember, redirectTo: returnTo })
        // A signed-in answer has already navigated. A fork moves this mount
        // to its next phase — the bridge lives here now.
        if (outcome.kind === 'mfa-challenge') {
          setPhase({ name: 'challenge', challenge: outcome })
        } else if (outcome.kind === 'enrollment-required') {
          setPhase({ name: 'enrollment', challenge: outcome })
        }
      } catch (error: unknown) {
        setFailed(getErrorMessage(error))
      }
    }
  })

  const restartSignIn = () => {
    setPhase({ name: 'credentials' })
  }

  const signInWithPasskey = async () => {
    setFailed(null)
    setPasskeyBusy(true)
    try {
      // The assertion resolves an account the credential names — discoverable
      // sign-in. The context takes custody and navigates.
      const assertion = await runPasskeyAssertion(transport)
      await verifyPasskeyLogin(assertion.sessionId, assertion.credential, {
        redirectTo: returnTo
      })
    } catch (error: unknown) {
      setFailed(getErrorMessage(error))
    } finally {
      setPasskeyBusy(false)
    }
  }

  // The email request always answers success — the address list is not the
  // page's to disclose — and carries the device token the exchange demands
  // back. Both travel to the code phase in this mount's memory alone.
  const requestOneTimeCode = async (email: string) => {
    setFailed(null)
    try {
      const deviceToken = await requestOneTimeAccess(email)
      setPhase({
        name: 'one-time-code',
        deviceToken: deviceToken || undefined,
        notice: 'Check your inbox'
      })
    } catch (error: unknown) {
      setFailed(getErrorMessage(error))
    }
  }

  const signInWithOneTimeCode = async (code: string, deviceToken?: string) => {
    setFailed(null)
    try {
      const outcome = await exchangeOneTimeToken(code, deviceToken)
      // A signed-in answer has already navigated. A fork moves this mount
      // to its next phase — the same machine the credentials form feeds.
      if (outcome.kind === 'mfa-challenge') {
        setPhase({ name: 'challenge', challenge: outcome })
      } else if (outcome.kind === 'enrollment-required') {
        setPhase({ name: 'enrollment', challenge: outcome })
      }
    } catch (error: unknown) {
      setFailed(getErrorMessage(error))
    }
  }

  if (phase.name === 'challenge') {
    return (
      <MfaChallengeView challenge={phase.challenge} returnTo={returnTo} onRestart={restartSignIn} />
    )
  }

  if (phase.name === 'enrollment') {
    return (
      <MfaEnrollmentView
        challenge={phase.challenge}
        returnTo={returnTo}
        onRestart={restartSignIn}
      />
    )
  }

  if (phase.name === 'one-time-code') {
    return (
      <OneTimeCodeEntry
        notice={phase.notice}
        failed={failed}
        onFailed={setFailed}
        onDismissFailed={clearAlerts}
        onSubmit={(code) => void signInWithOneTimeCode(code, phase.deviceToken)}
        onRestart={restartSignIn}
      />
    )
  }

  return (
    <div {...stylex.props(styles.page)}>
      {/* Page-level notices live above the card, matching its width. */}
      {(failed || showGoodbye || showSignInPrompt || showReset) && (
        <div {...stylex.props(styles.alerts)}>
          {failed && (
            <ViewTransition>
              <Alert variant='destructive' id='login-alert-error'>
                <AlertTitle>Sign in failed</AlertTitle>
                <AlertDescription>{failed}</AlertDescription>
              </Alert>
            </ViewTransition>
          )}
          {showGoodbye && (
            <ViewTransition>
              <Alert id='login-alert-goodbye'>
                <AlertTitle>Goodbye!</AlertTitle>
                <AlertDescription>Your session has been terminated.</AlertDescription>
              </Alert>
            </ViewTransition>
          )}
          {showSignInPrompt && (
            <ViewTransition>
              <Alert id='login-alert-signin'>
                <AlertTitle>Sign in required</AlertTitle>
                <AlertDescription>You are unauthenticated. Sign in to continue.</AlertDescription>
              </Alert>
            </ViewTransition>
          )}
          {showReset && (
            <ViewTransition>
              <Alert id='login-alert-reset'>
                <AlertTitle>Password reset</AlertTitle>
                <AlertDescription>
                  Your password has been reset — sign in with the new one.
                </AlertDescription>
              </Alert>
            </ViewTransition>
          )}
        </div>
      )}

      <Card size='md' id='login-card' style={styles.cardRoot}>
        <CardHeader style={styles.header}>
          <div {...stylex.props(styles.logo)}>
            <ViteIcon size={28} />
          </div>
          <Text render={<h1 />} variant='featured-5' weight='semibold'>
            Sign in to your account
          </Text>
          <CardDescription>Welcome back! Please enter your credentials.</CardDescription>
        </CardHeader>

        <CardContent>
          {/* One button per connection the store enabled; the configuration's
              master switch gates the whole block. The start route is the
              server's own — the 302 chain is the flow's transport. */}
          {config?.oauth.enabled && providers?.connections?.length ? (
            <>
              <ButtonGroup orientation='vertical' style={styles.socialGroup}>
                {providers.connections.map((conn) => {
                  const Icon = PROVIDER_ICONS[conn.provider]
                  return (
                    <Button
                      key={conn.provider}
                      type='button'
                      variant='outline'
                      style={socialStyles.socialButton}
                      render={<a href={`/oauth/${conn.provider}/start`} />}
                    >
                      {Icon && <Icon size={16} />}
                      {conn.displayName}
                    </Button>
                  )
                })}
              </ButtonGroup>
              <FieldSeparator style={styles.divider}>or continue with</FieldSeparator>
            </>
          ) : null}

          <Button
            type='button'
            variant='outline'
            disabled={passkeyBusy}
            onClick={() => void signInWithPasskey()}
            style={styles.passkeyButton}
          >
            {passkeyBusy && <Spinner />}
            {passkeyBusy ? 'Waiting for your authenticator…' : 'Sign in with a passkey'}
          </Button>

          <form
            id='login-form'
            autoComplete='on'
            onSubmit={(e) => {
              e.preventDefault()
              e.stopPropagation()
              void form.handleSubmit()
            }}
          >
            <div id='login-form-grid' {...stylex.props(styles.formGrid)}>
              <form.Field
                name='identity'
                validators={{
                  onChange: ({ value }) => (value ? undefined : { message: 'Identity is required' })
                }}
                children={(field) => {
                  const error = field.state.meta.errors?.[0]?.message
                  return (
                    <Field id='field-identity' invalid={!!error}>
                      <FieldLabel htmlFor='identity'>Username or email</FieldLabel>
                      <Input
                        id='identity'
                        name='identity'
                        placeholder='emilys'
                        autoComplete='username'
                        value={field.state.value}
                        onChange={(e) => {
                          clearAlerts()
                          field.handleChange(e.target.value)
                        }}
                        onBlur={field.handleBlur}
                      />
                      <FieldError errors={error ? [{ message: error }] : undefined} />
                    </Field>
                  )
                }}
              />

              <form.Field
                name='password'
                validators={{
                  onChange: ({ value }) => (value ? undefined : { message: 'Password is required' })
                }}
                children={(field) => {
                  const error = field.state.meta.errors?.[0]?.message
                  return (
                    <Field id='field-password' invalid={!!error}>
                      <div {...stylex.props(styles.labelRow)}>
                        <FieldLabel htmlFor='password'>Password</FieldLabel>
                        <Link to='/forgot-password' {...stylex.props(styles.forgotLink)}>
                          Forgot password?
                        </Link>
                      </div>
                      <InputPassword
                        id='password'
                        name='password'
                        placeholder='••••••••'
                        autoComplete='current-password'
                        value={field.state.value}
                        onChange={(e) => {
                          clearAlerts()
                          field.handleChange(e.target.value)
                        }}
                        onBlur={field.handleBlur}
                      />
                      <FieldError errors={error ? [{ message: error }] : undefined} />
                    </Field>
                  )
                }}
              />
            </div>

            <Field orientation='horizontal' style={styles.rememberField}>
              <Checkbox
                id='remember'
                name='remember'
                checked={remember}
                onCheckedChange={(checked) => setRemember(checked)}
              />
              <FieldLabel htmlFor='remember'>Remember me on this device</FieldLabel>
            </Field>

            <div {...stylex.props(styles.submitWrapper)}>
              <form.Subscribe
                selector={(state) => [state.canSubmit, state.isSubmitting] as const}
                children={([canSubmit, isSubmitting]) => (
                  <Button
                    type='submit'
                    variant='primary'
                    disabled={!canSubmit}
                    style={styles.submit}
                  >
                    {isSubmitting && <Spinner />}
                    {isSubmitting ? (
                      <LoaderText variant='body-2'>Signing in…</LoaderText>
                    ) : (
                      'Sign in'
                    )}
                  </Button>
                )}
              />
            </div>

            {/* The one-time entry rides the deployment's public toggle: an
                absent query would ask the server for a surface it may not
                serve. The email field doubles as the address the code goes
                to — one form, two asks. */}
            {oneTimeEnabled && (
              <Button
                type='button'
                variant='ghost'
                onClick={() => {
                  const email = form.getFieldValue('identity').trim()
                  const looksLikeEmail = /.+@.+\..+/.test(email)
                  if (!looksLikeEmail) {
                    setFailed('Enter your email address first, then request the code.')
                    return
                  }
                  void requestOneTimeCode(email)
                }}
                style={styles.ghostEntry}
              >
                Email me a one-time sign-in code
              </Button>
            )}
          </form>
        </CardContent>

        <CardFooter style={atoms.justifyContent.center}>
          <Text variant='body-2' color='neutral-faded'>
            Back to{' '}
            <Link to='/' {...stylex.props(styles.backLink)}>
              homepage
            </Link>
          </Text>
        </CardFooter>
      </Card>
    </div>
  )
}
