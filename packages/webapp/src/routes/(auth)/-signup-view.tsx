import { useMutation } from '@connectrpc/connect-query'
import atoms from '@stylexjs/atoms'
import * as stylex from '@stylexjs/stylex'
import { useNavigate } from '@tanstack/react-router'
import { Link } from '@tanstack/react-router'
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
import { ViteIcon } from 'uilibs/components/icons'
import { usePublicSettings } from '#/hooks/use-public-settings'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { styles } from '#/styles/pages/login.stylex'
import { SignupService } from '~/codegen/identity_pb'

/** The column's check mirrors: 3-32 ASCII letters, digits, and underscores. */
const USERNAME_PATTERN = /^[a-zA-Z0-9_]{3,32}$/

/**
 * The public sign-up (phase 4): the deployment's settings own the shape, so
 * the form renders from `SettingsService/ListPublic` alone — the access mode
 * decides the invitation token, the identity toggles decide the username,
 * and the verification gate decides where a created account lands. A gate
 * the settings have not answered yet is a form not shown.
 */
export function SignupView({ prefill }: { prefill?: string }) {
  const navigate = useNavigate()
  const settings = usePublicSettings()

  const inviteMode = settings.get('access.mode') !== 'open'
  const usernameOn = settings.isOn('auth.signup_username_enabled')
  const usernameRequired = usernameOn && settings.isOn('auth.require_username')
  const verifyAtSignup = settings.isOn('auth.verify_email_at_signup')

  const [firstName, setFirstName] = useState('')
  const [lastName, setLastName] = useState('')
  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [token, setToken] = useState(prefill ?? '')
  const [failed, setFailed] = useState<string | null>(null)

  const signingUp = useMutation(SignupService.method.signup, {
    // The verification gate decides the landing: an armed gate leaves the
    // account unverified with its code in the mail, so the reader goes to
    // the code entry; a disarmed one created a verified account ready to
    // sign in.
    onSuccess: () => {
      setFailed(null)
      void navigate({ to: verifyAtSignup ? '/verify-email' : '/login' })
    },
    onError: (error) => setFailed(getErrorMessage(error))
  })

  const submit = () => {
    setFailed(null)
    if (!firstName.trim() || !lastName.trim()) {
      setFailed('Enter your first and last name.')
      return
    }
    if (usernameRequired && !username.trim()) {
      setFailed('Username is required.')
      return
    }
    if (username.trim() && !USERNAME_PATTERN.test(username.trim())) {
      setFailed('Username may use letters, digits, and underscores (3–32 characters).')
      return
    }
    if (!/.+@.+\..+/.test(email.trim())) {
      setFailed('Enter a valid email address.')
      return
    }
    if (password.length < 8) {
      setFailed('Your password must be at least 8 characters.')
      return
    }
    if (inviteMode && !token.trim()) {
      setFailed('Enter your invitation token.')
      return
    }
    signingUp.mutate({
      firstName: firstName.trim(),
      lastName: lastName.trim(),
      username: username.trim(),
      email: email.trim(),
      password,
      token: token.trim()
    })
  }

  if (settings.isLoading) {
    return (
      <Card size='md' style={styles.cardRoot}>
        <CardContent {...stylex.props(localStyles.loadingBox)}>
          <Spinner />
        </CardContent>
      </Card>
    )
  }

  return (
    <Card size='md' style={styles.cardRoot}>
      <CardHeader style={styles.header}>
        <div {...stylex.props(styles.logo)}>
          <ViteIcon size={28} />
        </div>
        <Text render={<h1 />} variant='featured-5' weight='semibold'>
          Create your account
        </Text>
        <CardDescription>
          {inviteMode
            ? 'This deployment admits new accounts by invitation — paste the token your invitation carries.'
            : 'Join this deployment with your name, address, and a password.'}
        </CardDescription>
      </CardHeader>

      <CardContent>
        <form
          id='signup-form'
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            submit()
          }}
        >
          <div {...stylex.props(styles.formGrid)}>
            {failed ? (
              <Alert variant='destructive' id='signup-error'>
                <AlertTitle>Sign up failed</AlertTitle>
                <AlertDescription>{failed}</AlertDescription>
              </Alert>
            ) : null}

            <div {...stylex.props(signupNameRow.root)}>
              <Field id='signup-first-name'>
                <FieldLabel htmlFor='signup-first-name'>First name</FieldLabel>
                <Input
                  id='signup-first-name'
                  name='first-name'
                  autoComplete='given-name'
                  value={firstName}
                  onChange={(e) => {
                    setFailed(null)
                    setFirstName(e.target.value)
                  }}
                />
              </Field>
              <Field id='signup-last-name'>
                <FieldLabel htmlFor='signup-last-name'>Last name</FieldLabel>
                <Input
                  id='signup-last-name'
                  name='last-name'
                  autoComplete='family-name'
                  value={lastName}
                  onChange={(e) => {
                    setFailed(null)
                    setLastName(e.target.value)
                  }}
                />
              </Field>
            </div>

            {usernameOn ? (
              <Field id='signup-username'>
                <FieldLabel htmlFor='signup-username'>
                  Username{usernameRequired ? '' : ' (optional)'}
                </FieldLabel>
                <Input
                  id='signup-username'
                  name='username'
                  autoComplete='username'
                  value={username}
                  onChange={(e) => {
                    setFailed(null)
                    setUsername(e.target.value)
                  }}
                />
                <FieldDescription>Letters, digits, and underscores.</FieldDescription>
              </Field>
            ) : null}

            <Field id='signup-email'>
              <FieldLabel htmlFor='signup-email'>Email</FieldLabel>
              <Input
                id='signup-email'
                name='email'
                type='email'
                autoComplete='email'
                placeholder='you@example.com'
                value={email}
                onChange={(e) => {
                  setFailed(null)
                  setEmail(e.target.value)
                }}
              />
            </Field>

            <Field id='signup-password'>
              <FieldLabel htmlFor='signup-password'>Password</FieldLabel>
              <InputPassword
                id='signup-password'
                name='password'
                autoComplete='new-password'
                placeholder='••••••••'
                value={password}
                onChange={(e) => {
                  setFailed(null)
                  setPassword(e.target.value)
                }}
              />
              <FieldDescription>At least 8 characters.</FieldDescription>
            </Field>

            {inviteMode ? (
              <Field id='signup-token'>
                <FieldLabel htmlFor='signup-token'>Invitation token</FieldLabel>
                <Input
                  id='signup-token'
                  name='token'
                  value={token}
                  onChange={(e) => {
                    setFailed(null)
                    setToken(e.target.value)
                  }}
                />
              </Field>
            ) : null}

            <Button type='submit' variant='primary' disabled={signingUp.isPending}>
              {signingUp.isPending && <Spinner />}
              {signingUp.isPending ? (
                <LoaderText variant='body-2'>Creating your account…</LoaderText>
              ) : (
                'Create account'
              )}
            </Button>
          </div>
        </form>
      </CardContent>

      <CardFooter style={atoms.justifyContent.center}>
        <Text variant='body-2' color='neutral-faded'>
          Already have an account?{' '}
          <Link to='/login' {...stylex.props(styles.forgotLink)}>
            Sign in
          </Link>
        </Text>
      </CardFooter>
    </Card>
  )
}

const signupNameRow = stylex.create({
  root: {
    display: 'flex',
    gap: '1rem'
  }
})

const localStyles = stylex.create({
  loadingBox: {
    alignItems: 'center',
    display: 'flex',
    justifyContent: 'center',
    minHeight: '12rem'
  }
})
