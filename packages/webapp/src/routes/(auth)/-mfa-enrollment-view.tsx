import { create } from '@bufbuild/protobuf'
import { createClient } from '@connectrpc/connect'
import { useTransport } from '@connectrpc/connect-query'
import * as stylex from '@stylexjs/stylex'
import { useForm } from '@tanstack/react-form'
import { useMemo, useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Field, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { Alert, AlertDescription, AlertTitle } from 'uilibs/components/extra/alert'
import { Card, CardContent, CardDescription, CardHeader } from 'uilibs/components/extra/card'
import { LoaderText } from 'uilibs/components/extra/loader-text'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { renderSVG } from 'uqr'
import { useAuthentication } from '#/hooks/use-auth'
import type { SignInChallenge } from '#/libraries/guard/auth-engine'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { styles } from '#/styles/pages/login.stylex'
import {
  BeginTotpEnrollmentRequestSchema,
  ConfirmTotpEnrollmentRequestSchema,
  MultifactorService
} from '~/codegen/authn_pb'
import { useBridgeCountdown } from './-mfa-challenge-view'

const mfaStyles = stylex.create({
  qr: {
    display: 'flex',
    justifyContent: 'center',
    padding: 8,
    background: '#fff',
    borderRadius: 8,
    width: 192,
    marginInline: 'auto'
  },
  secret: {
    fontFamily: 'monospace',
    wordBreak: 'break-all',
    textAlign: 'center'
  },
  codes: {
    display: 'grid',
    gridTemplateColumns: 'repeat(2, 1fr)',
    gap: 8,
    fontFamily: 'monospace'
  },
  stepStack: {
    display: 'flex',
    flexDirection: 'column',
    gap: 12
  }
})

/**
 * The forced-enrollment phase: the account kept no confirmed factor, so the
 * bridge admits the enrollment endpoints instead of the challenge. The
 * secret's only appearance is the begin answer; the recovery codes' only
 * appearance is the confirm answer. The sign-in completes through the
 * challenge's bridge once the codes are acknowledged.
 */
export function MfaEnrollmentView({
  challenge,
  returnTo,
  onRestart
}: {
  challenge: SignInChallenge
  returnTo?: string
  onRestart: () => void
}) {
  const { completeSignIn } = useAuthentication()
  // The enrollment rides the provided transport: the bridge travels as the
  // request's own field — the call holds no session credential, so the
  // worker's custody seam has nothing to add here.
  const transport = useTransport()
  const mfaRpc = useMemo(() => createClient(MultifactorService, transport), [transport])
  const secondsLeft = useBridgeCountdown(challenge.expiresAt)
  const expired = secondsLeft <= 0

  const [deviceName, setDeviceName] = useState('This browser')
  const [enrolling, setEnrolling] = useState(false)
  const [scan, setScan] = useState<{
    totpId: string
    secret: string
    otpauthUri: string
  } | null>(null)
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)
  const [confirmedCode, setConfirmedCode] = useState<string | null>(null)
  const [failed, setFailed] = useState<string | null>(null)
  const [completing, setCompleting] = useState(false)

  const confirmForm = useForm({
    defaultValues: { code: '' },
    onSubmit: async ({ value }) => {
      if (!scan) return
      setFailed(null)
      try {
        const confirmed = await mfaRpc.confirmTotpEnrollment(
          create(ConfirmTotpEnrollmentRequestSchema, {
            totpId: scan.totpId,
            code: value.code,
            pendingToken: challenge.pendingToken
          })
        )
        setConfirmedCode(value.code)
        // The clear recovery codes exist in component state alone — the
        // backend keeps hashes, and nothing re-renders them later.
        setRecoveryCodes(confirmed.recoveryCodes)
      } catch (error: unknown) {
        setFailed(getErrorMessage(error))
      }
    }
  })

  const beginEnrollment = async () => {
    setFailed(null)
    setEnrolling(true)
    try {
      const begin = await mfaRpc.beginTotpEnrollment(
        create(BeginTotpEnrollmentRequestSchema, {
          name: deviceName,
          pendingToken: challenge.pendingToken
        })
      )
      setScan({
        totpId: begin.totpId,
        secret: begin.secret,
        otpauthUri: begin.otpauthUri
      })
    } catch (error: unknown) {
      setFailed(getErrorMessage(error))
    } finally {
      setEnrolling(false)
    }
  }

  const finishSignIn = async () => {
    if (!confirmedCode) return
    setCompleting(true)
    setFailed(null)
    try {
      // The confirm did not spend the code's time step — the enrollment is
      // not a sign-in proof — so the same code the authenticator just
      // rendered completes the sign-in through the bridge.
      await completeSignIn(
        challenge.pendingToken,
        { kind: 'code', code: confirmedCode },
        { redirectTo: returnTo }
      )
    } catch (error: unknown) {
      setFailed(getErrorMessage(error))
      setCompleting(false)
    }
  }

  return (
    <div {...stylex.props(styles.page)}>
      <Card size='md' id='mfa-enrollment-card' style={styles.cardRoot}>
        <CardHeader style={styles.header}>
          <Text render={<h1 />} variant='featured-5' weight='semibold'>
            Set up two-factor authentication
          </Text>
          <CardDescription>
            This account requires a second factor. Enroll your authenticator to finish signing in.
          </CardDescription>
        </CardHeader>

        <CardContent>
          {expired ? (
            <>
              <Alert variant='destructive' id='mfa-enrollment-expired'>
                <AlertTitle>The enrollment window closed</AlertTitle>
                <AlertDescription>
                  The sign-in attempt expired. Sign in again to continue.
                </AlertDescription>
              </Alert>
              <Button variant='outline' onClick={onRestart} style={styles.submit}>
                Back to sign in
              </Button>
            </>
          ) : !scan ? (
            <div {...stylex.props(mfaStyles.stepStack)}>
              <Field id='field-mfa-name'>
                <FieldLabel htmlFor='mfa-device-name'>Name this device</FieldLabel>
                <Input
                  id='mfa-device-name'
                  value={deviceName}
                  onChange={(e) => setDeviceName(e.target.value)}
                  placeholder='Aegis on tablet'
                />
              </Field>
              <Button
                variant='primary'
                disabled={enrolling || deviceName.trim().length === 0}
                onClick={() => void beginEnrollment()}
                style={styles.submit}
              >
                {enrolling && <Spinner />}
                {enrolling ? <LoaderText variant='body-2'>Preparing…</LoaderText> : 'Continue'}
              </Button>
            </div>
          ) : !recoveryCodes ? (
            <div {...stylex.props(mfaStyles.stepStack)}>
              <div
                id='mfa-enrollment-qr'
                {...stylex.props(mfaStyles.qr)}
                dangerouslySetInnerHTML={{ __html: renderSVG(scan.otpauthUri) }}
              />
              <Text variant='body-2' color='neutral-faded'>
                Scan the QR code with your authenticator app, or enter the secret by hand:
              </Text>
              <Text variant='body-2' style={mfaStyles.secret}>
                {scan.secret}
              </Text>
              <confirmForm.Field
                name='code'
                validators={{
                  onChange: ({ value }) =>
                    /^[0-9]{6,8}$/.test(value)
                      ? undefined
                      : { message: 'Enter the 6 to 8 digit code' }
                }}
                children={(field) => {
                  const error = field.state.meta.errors?.[0]?.message
                  return (
                    <Field id='field-mfa-confirm-code' invalid={!!error}>
                      <FieldLabel htmlFor='mfa-confirm-code'>
                        The code your authenticator renders
                      </FieldLabel>
                      <input
                        id='mfa-confirm-code'
                        inputMode='numeric'
                        autoComplete='one-time-code'
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
                <Alert variant='destructive' id='mfa-enrollment-error'>
                  <AlertTitle>Enrollment failed</AlertTitle>
                  <AlertDescription>{failed}</AlertDescription>
                </Alert>
              ) : null}
              <confirmForm.Subscribe
                selector={(state) => [state.canSubmit, state.isSubmitting] as const}
                children={([canSubmit, isSubmitting]) => (
                  <Button
                    type='submit'
                    variant='primary'
                    disabled={!canSubmit}
                    style={styles.submit}
                    onClick={() => void confirmForm.handleSubmit()}
                  >
                    {isSubmitting && <Spinner />}
                    {isSubmitting ? (
                      <LoaderText variant='body-2'>Confirming…</LoaderText>
                    ) : (
                      'Confirm'
                    )}
                  </Button>
                )}
              />
            </div>
          ) : (
            <div {...stylex.props(mfaStyles.stepStack)}>
              <Alert id='mfa-recovery-codes'>
                <AlertTitle>Your recovery codes — shown once</AlertTitle>
                <AlertDescription>
                  Store them somewhere safe. This is the only time they are displayed; the account
                  keeps only their hashes.
                </AlertDescription>
              </Alert>
              <div id='mfa-recovery-codes-list' {...stylex.props(mfaStyles.codes)}>
                {recoveryCodes.map((code) => (
                  <Text key={code} variant='body-2' style={mfaStyles.secret}>
                    {code}
                  </Text>
                ))}
              </div>
              <Button
                variant='primary'
                disabled={completing}
                onClick={() => void finishSignIn()}
                style={styles.submit}
              >
                {completing && <Spinner />}
                {completing ? (
                  <LoaderText variant='body-2'>Completing sign-in…</LoaderText>
                ) : (
                  'I saved my recovery codes'
                )}
              </Button>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
