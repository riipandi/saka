import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import { createClient } from '@connectrpc/connect'
import { createConnectQueryKey, useQuery, useTransport } from '@connectrpc/connect-query'
import { KeyRound, Plus, Smartphone, Check, Copy } from '@keyline-icons/react'
import { startRegistration } from '@simplewebauthn/browser'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo, useState } from 'react'
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
import { Field, FieldDescription, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { OTPField, OTPFieldSlot } from 'uilibs/components/base/otp-field'
import { Alert, AlertDescription, AlertTitle } from 'uilibs/components/extra/alert'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import { InputPassword } from 'uilibs/components/extra/input-password'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { renderSVG } from 'uqr'
import { FormSkeleton } from '#/components/-skeleton-form'
import { TableSkeleton } from '#/components/-skeleton-rows'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { ReauthenticateDialog } from '#/libraries/reauth/-reauthenticate-dialog'
import { StepUpCancelled } from '#/libraries/reauth/reauth'
import { useStepUp } from '#/libraries/reauth/use-step-up'
import { pageStyles } from '#/styles/pages/page.stylex'
import { styles } from '#/styles/pages/security.stylex'
import {
  DeleteTotpEnrollmentRequestSchema,
  BeginTotpEnrollmentRequestSchema,
  ConfirmTotpEnrollmentRequestSchema,
  DisableMfaRequestSchema,
  ListTotpEnrollmentsRequestSchema,
  MultifactorService,
  RegenerateRecoveryCodesRequestSchema
} from '~/codegen/authn_pb'
import { GetCurrentUserRequestSchema, UserService } from '~/codegen/identity_pb'
import { AddPasswordRequestSchema, RemovePasswordRequestSchema } from '~/codegen/identity_pb'
import {
  BeginRegistrationRequestSchema,
  DeleteCredentialRequestSchema,
  ListCredentialsRequestSchema,
  UpdateCredentialRequestSchema,
  VerifyRegistrationRequestSchema,
  WebAuthnService
} from '~/codegen/webauthn_pb'
import { AccountFrame } from './-account-frame'

/** The instants the security page renders — read as the viewer's wall
 * clock, the way the session list reads its own. The wire's instants arrive
 * as proto Timestamps; the account's password instant arrives as the
 * RFC 3339 string the wire view carries. */
const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatInstant(value: Date | string | undefined): string {
  if (!value) return '—'
  const instant = value instanceof Date ? value : new Date(value)
  return Number.isNaN(instant.getTime()) ? '—' : instantFormat.format(instant)
}

/** The floor the add-password form asks before the backend judges the
 * policy — the backend's settings own the real bound, this is the hint. */
const PASSWORD_HINT_MIN = 8

/**
 * The one-slot code request the guarded MFA actions ride: after the
 * reauthentication proof, the backend's body contract asks for a code from
 * a confirmed authenticator or a recovery code. The promise resolves with
 * what the caller typed; a dismissal rejects and the whole action stops.
 */
function useCodeRequest() {
  const [pending, setPending] = useState<{
    resolve: (code: string) => void
    reject: (reason: unknown) => void
  } | null>(null)

  const request = useCallback(
    () =>
      new Promise<string>((resolve, reject) => {
        setPending({ resolve, reject })
      }),
    []
  )

  const submit = useCallback(
    (code: string) => {
      pending?.resolve(code)
      setPending(null)
    },
    [pending]
  )

  const cancel = useCallback(() => {
    pending?.reject(new StepUpCancelled())
    setPending(null)
  }, [pending])

  return { request, open: pending !== null, submit, cancel }
}

interface CodeRequestDialogProps {
  open: boolean
  title: string
  description: string
  optional?: boolean
  onSubmit: (code: string) => void
  onCancel: () => void
}

/** The second-factor code prompt the MFA writes share. */
function CodeRequestDialog({
  open,
  title,
  description,
  optional,
  onSubmit,
  onCancel
}: CodeRequestDialogProps) {
  const [code, setCode] = useState('')
  const valid = optional || code.length >= 6
  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setCode('')
          onCancel()
        }
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <Field id='field-factor-code'>
          <FieldLabel htmlFor='factor-code'>Authenticator or recovery code</FieldLabel>
          <Input
            id='factor-code'
            value={code}
            onChange={(e) => setCode(e.target.value)}
            autoComplete='one-time-code'
          />
          {optional ? (
            <FieldDescription>Leave empty when no other confirmed factor is held.</FieldDescription>
          ) : null}
        </Field>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={!valid}
            onClick={() => {
              onSubmit(code)
              setCode('')
            }}
          >
            Continue
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/**
 * The security center: the account's credentials and second factors. Every
 * destructive write asks twice in the shape the backend demands — the
 * reauthentication proof rides the `X-Saka-Reauthentication` header, and the
 * MFA writes carry a factor code in the body. The dialogs chain: proof
 * first, code second, the call spends both.
 */
export function SecurityView({ embedded = false }: { embedded?: boolean } = {}) {
  const queryClient = useQueryClient()
  const transport = useTransport()

  const account = useQuery(UserService.method.getCurrentUser, create(GetCurrentUserRequestSchema))
  const enrollments = useQuery(
    MultifactorService.method.listTotpEnrollments,
    create(ListTotpEnrollmentsRequestSchema)
  )
  const passkeys = useQuery(
    WebAuthnService.method.listCredentials,
    create(ListCredentialsRequestSchema)
  )

  const userKey = createConnectQueryKey({
    schema: UserService.method.getCurrentUser,
    input: create(GetCurrentUserRequestSchema),
    cardinality: 'finite'
  })
  const listKey = createConnectQueryKey({
    schema: MultifactorService.method.listTotpEnrollments,
    input: create(ListTotpEnrollmentsRequestSchema),
    cardinality: 'finite'
  })

  const stepUp = useStepUp()
  const factorCode = useCodeRequest()
  const [actionError, setActionError] = useState<string | null>(null)
  const [regeneratedCodes, setRegeneratedCodes] = useState<string[] | null>(null)
  const [newPassword, setNewPassword] = useState('')
  const [newPasswordConfirm, setNewPasswordConfirm] = useState('')
  const [working, setWorking] = useState(false)
  const [passkeyDialog, setPasskeyDialog] = useState<
    { kind: 'enroll' } | { kind: 'rename'; credentialId: string; name: string } | null
  >(null)
  const [passkeyName, setPasskeyName] = useState('')
  const [passkeyBusy, setPasskeyBusy] = useState(false)
  // The authenticator enrollment is an inline multistep panel, not a
  // dialog: the name step mints the unconfirmed row, the scan step proves
  // it with a code, and the last step shows the recovery codes once.
  const [enrollOpen, setEnrollOpen] = useState(false)
  const [enrollStep, setEnrollStep] = useState<'name' | 'scan' | 'codes'>('name')
  const [enrollName, setEnrollName] = useState('')
  const [totpScan, setTotpScan] = useState<{
    totpId: string
    secret: string
    otpauthUri: string
  } | null>(null)
  const [enrollCode, setEnrollCode] = useState('')
  const [enrollCodes, setEnrollCodes] = useState<string[] | null>(null)
  const [enrollError, setEnrollError] = useState<string | null>(null)
  const [enrollBusy, setEnrollBusy] = useState(false)
  // The copy affordance's flash: the codes leave once, the button says so.
  const [codesCopied, setCodesCopied] = useState(false)

  const copyRecoveryCodes = async (codes: string[]) => {
    await navigator.clipboard.writeText(codes.join('\n'))
    setCodesCopied(true)
    window.setTimeout(() => setCodesCopied(false), 2000)
  }

  const userRpc = useMemo(() => createClient(UserService, transport), [transport])
  const mfaRpc = useMemo(() => createClient(MultifactorService, transport), [transport])
  const webauthnRpc = useMemo(() => createClient(WebAuthnService, transport), [transport])

  const hasPassword = Boolean(account.data?.user?.passwordUpdatedAt)

  const swallowCancelled = (error: unknown) => {
    if (error instanceof StepUpCancelled) return
    setActionError(getErrorMessage(error))
  }

  const afterWrite = () => {
    setActionError(null)
    void queryClient.invalidateQueries({ queryKey: userKey })
    void queryClient.invalidateQueries({ queryKey: listKey })
    void queryClient.invalidateQueries({
      queryKey: createConnectQueryKey({
        schema: WebAuthnService.method.listCredentials,
        input: create(ListCredentialsRequestSchema),
        cardinality: 'finite'
      })
    })
  }

  const addPassword = () => {
    setWorking(true)
    stepUp
      .run(async (options) => {
        await userRpc.addPassword(create(AddPasswordRequestSchema, { newPassword }), options)
      })
      .then(() => {
        setNewPassword('')
        setNewPasswordConfirm('')
        afterWrite()
      })
      .catch(swallowCancelled)
      .finally(() => setWorking(false))
  }

  const removePassword = () => {
    setWorking(true)
    stepUp
      .run(async (options) => {
        await userRpc.removePassword(create(RemovePasswordRequestSchema), options)
      })
      .then(afterWrite)
      .catch(swallowCancelled)
      .finally(() => setWorking(false))
  }

  const deleteEnrollment = (totpId: string, needsCode: boolean) => {
    setWorking(true)
    const remove = async (code: string) => {
      await mfaRpc.deleteTotpEnrollment(create(DeleteTotpEnrollmentRequestSchema, { totpId, code }))
    }
    if (needsCode) {
      factorCode
        .request()
        .then((code) => remove(code))
        .then(afterWrite)
        .catch(swallowCancelled)
        .finally(() => setWorking(false))
      return
    }
    remove('')
      .then(afterWrite)
      .catch(swallowCancelled)
      .finally(() => setWorking(false))
  }

  const resetEnrollment = () => {
    setEnrollOpen(false)
    setEnrollStep('name')
    setEnrollName('')
    setTotpScan(null)
    setEnrollCode('')
    setEnrollCodes(null)
    setEnrollError(null)
    setEnrollBusy(false)
    setCodesCopied(false)
  }

  const finishEnrollment = () => {
    // The codes were shown once and acknowledged; the panel folds and the
    // roll refetches to show the confirmed row.
    resetEnrollment()
    afterWrite()
  }

  const cancelEnrollment = () => {
    // A begun-but-unconfirmed row is a mistyped start — the delete of one
    // costs no code. The panel closes either way.
    if (totpScan) {
      mfaRpc
        .deleteTotpEnrollment(
          create(DeleteTotpEnrollmentRequestSchema, { totpId: totpScan.totpId, code: '' })
        )
        .then(afterWrite)
        .catch(swallowCancelled)
    }
    resetEnrollment()
  }

  const beginTotpEnrollment = async () => {
    setEnrollBusy(true)
    setEnrollError(null)
    try {
      // The signed-in caller omits the pending token — the access token
      // stands in for the bridge the sign-in mints.
      const begin = await mfaRpc.beginTotpEnrollment(
        create(BeginTotpEnrollmentRequestSchema, { name: enrollName.trim() })
      )
      setTotpScan({ totpId: begin.totpId, secret: begin.secret, otpauthUri: begin.otpauthUri })
      setEnrollStep('scan')
    } catch (error: unknown) {
      setEnrollError(getErrorMessage(error))
    } finally {
      setEnrollBusy(false)
    }
  }

  const confirmTotpEnrollment = async () => {
    if (!totpScan) return
    setEnrollBusy(true)
    setEnrollError(null)
    try {
      const confirmed = await mfaRpc.confirmTotpEnrollment(
        create(ConfirmTotpEnrollmentRequestSchema, { totpId: totpScan.totpId, code: enrollCode })
      )
      setEnrollCodes([...confirmed.recoveryCodes])
      setEnrollStep('codes')
      afterWrite()
    } catch (error: unknown) {
      setEnrollError(getErrorMessage(error))
    } finally {
      setEnrollBusy(false)
    }
  }

  const regenerateCodes = () => {
    setWorking(true)
    stepUp
      .run(async (options) => {
        // The proof rides the header; the body's code proves the caller
        // holds a factor — the two contracts land on the same call.
        const code = await factorCode.request()
        const answer = await mfaRpc.regenerateRecoveryCodes(
          create(RegenerateRecoveryCodesRequestSchema, { code }),
          options
        )
        setRegeneratedCodes([...answer.recoveryCodes])
      })
      .then(afterWrite)
      .catch(swallowCancelled)
      .finally(() => setWorking(false))
  }

  const disableMfa = () => {
    setWorking(true)
    stepUp
      .run(async (options) => {
        const code = await factorCode.request()
        await mfaRpc.disableMfa(create(DisableMfaRequestSchema, { code }), options)
      })
      .then(afterWrite)
      .catch(swallowCancelled)
      .finally(() => setWorking(false))
  }

  const enrollPasskey = async () => {
    setPasskeyBusy(true)
    setActionError(null)
    try {
      const ceremony = await webauthnRpc.beginRegistration(create(BeginRegistrationRequestSchema))
      const credential = await startRegistration({
        optionsJSON: JSON.parse(ceremony.options).publicKey
      })
      await webauthnRpc.verifyRegistration(
        create(VerifyRegistrationRequestSchema, {
          sessionId: ceremony.sessionId,
          credential: JSON.stringify(credential),
          name: passkeyName
        })
      )
      setPasskeyDialog(null)
      setPasskeyName('')
      afterWrite()
    } catch (error: unknown) {
      setActionError(getErrorMessage(error))
    } finally {
      setPasskeyBusy(false)
    }
  }

  const renamePasskey = async () => {
    if (passkeyDialog?.kind !== 'rename') return
    setPasskeyBusy(true)
    setActionError(null)
    try {
      await webauthnRpc.updateCredential(
        create(UpdateCredentialRequestSchema, {
          credentialId: passkeyDialog.credentialId,
          name: passkeyName
        })
      )
      setPasskeyDialog(null)
      setPasskeyName('')
      afterWrite()
    } catch (error: unknown) {
      setActionError(getErrorMessage(error))
    } finally {
      setPasskeyBusy(false)
    }
  }

  const deletePasskey = (credentialId: string) => {
    setWorking(true)
    stepUp
      .run(async (options) => {
        await webauthnRpc.deleteCredential(
          create(DeleteCredentialRequestSchema, { credentialId }),
          options
        )
      })
      .then(afterWrite)
      .catch(swallowCancelled)
      .finally(() => setWorking(false))
  }

  const passkeyRows = passkeys.data?.credentials ?? []

  const rows = enrollments.data?.enrollments ?? []
  const confirmedCount = rows.filter((row) => row.confirmedAt).length

  return (
    <AccountFrame
      embedded={embedded}
      title='Security'
      description='The credentials this account signs in with, and the second factor that guards them.'
    >
      <div {...stylex.props(pageStyles.stack)}>
        {actionError ? (
          <Text render={<p />} variant='body-2' color='critical' id='security-error'>
            {actionError}
          </Text>
        ) : null}

        <Card>
          <CardHeader>
            <CardTitle>Password</CardTitle>
            <CardDescription>
              {hasPassword
                ? `Last set ${formatInstant(account.data?.user?.passwordUpdatedAt)}.`
                : 'This account signs in without a password today.'}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {account.isPending ? (
              <FormSkeleton fields={3} />
            ) : hasPassword ? (
              <div {...stylex.props(pageStyles.stack)}>
                <Text variant='body-2' color='neutral-faded'>
                  Removing it leaves the other factors to carry sign-in — a passkey, or the one-time
                  codes the sign-in page offers.
                </Text>
                <Button variant='destructive' disabled={working} onClick={removePassword}>
                  Remove password
                </Button>
              </div>
            ) : (
              <div {...stylex.props(pageStyles.stack)}>
                <Field id='field-new-password'>
                  <FieldLabel htmlFor='new-password'>New password</FieldLabel>
                  <InputPassword
                    id='new-password'
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                    autoComplete='new-password'
                  />
                  <FieldDescription>
                    At least {PASSWORD_HINT_MIN} characters — the deployment's policy has the final
                    word.
                  </FieldDescription>
                </Field>
                <Field id='field-new-password-confirm'>
                  <FieldLabel htmlFor='new-password-confirm'>Confirm it</FieldLabel>
                  <InputPassword
                    id='new-password-confirm'
                    value={newPasswordConfirm}
                    onChange={(e) => setNewPasswordConfirm(e.target.value)}
                    autoComplete='new-password'
                  />
                </Field>
                <Button
                  variant='primary'
                  disabled={
                    working ||
                    newPassword.length < PASSWORD_HINT_MIN ||
                    newPassword !== newPasswordConfirm
                  }
                  onClick={addPassword}
                >
                  {working ? <Spinner /> : <KeyRound size={16} />}
                  Set password
                </Button>
              </div>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Authenticators</CardTitle>
            <CardDescription>
              The devices this account proves itself with at sign-in.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {enrollments.isPending ? (
              <TableSkeleton rows={3} />
            ) : rows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No authenticators</EmptyTitle>
                  <EmptyDescription>This account holds no second factor yet.</EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <div {...stylex.props(styles.listStack)}>
                {rows.map((row) => {
                  // The code is the removal's proof only when the removal
                  // would strip the LAST confirmed factor — the backend's
                  // own rule (`remaining <= 1` demands the code, a factor
                  // left standing deletes free). The old inverted predicate
                  // asked when factors stood and sent a bare code at the
                  // disarm — a 400.
                  const needsCode = Boolean(row.confirmedAt) && confirmedCount === 1
                  return (
                    <div key={row.totpId} {...stylex.props(styles.factorRow)}>
                      <Smartphone {...stylex.props(styles.factorIcon)} />
                      <div {...stylex.props(styles.factorMain)}>
                        <Text variant='body-2' weight='medium'>
                          {row.name}
                        </Text>
                        <Text variant='body-2' color='neutral-faded'>
                          {row.lastUsedAt
                            ? `Last used ${formatInstant(row.lastUsedAt && timestampDate(row.lastUsedAt))}`
                            : `Added ${formatInstant(row.createdAt && timestampDate(row.createdAt))}`}
                        </Text>
                      </div>
                      {row.confirmedAt ? (
                        <Badge variant='secondary'>Confirmed</Badge>
                      ) : (
                        <Badge variant='outline'>Pending</Badge>
                      )}
                      <Button
                        variant='outline'
                        size='sm'
                        disabled={working}
                        onClick={() => deleteEnrollment(row.totpId, needsCode)}
                      >
                        Remove
                      </Button>
                    </div>
                  )
                })}
              </div>
            )}
            <div {...stylex.props(styles.toolbarRow, styles.enrollRow)}>
              <Button
                variant='primary'
                disabled={working || enrollOpen}
                onClick={() => {
                  setEnrollName('')
                  setEnrollOpen(true)
                }}
              >
                <Plus size={16} />
                Add authenticator
              </Button>
            </div>
            {enrollOpen ? (
              // The enrollment is an inline multistep panel — the account
              // stays on the page, each finished step folds away, and the
              // cancel abandons the ceremony without touching a confirmed
              // factor.
              <div {...stylex.props(styles.enrollPanel)} data-testid='totp-enrollment-panel'>
                <Text render={<h4 />} variant='body-2' weight='semibold'>
                  {enrollStep === 'name'
                    ? 'Step 1 of 3 — name the device'
                    : enrollStep === 'scan'
                      ? 'Step 2 of 3 — scan and confirm'
                      : 'Step 3 of 3 — save the recovery codes'}
                </Text>
                {enrollStep === 'name' ? (
                  <Field id='field-totp-enroll-name'>
                    <FieldLabel htmlFor='totp-enroll-name'>Name this device</FieldLabel>
                    <Input
                      id='totp-enroll-name'
                      value={enrollName}
                      onChange={(e) => setEnrollName(e.target.value)}
                      placeholder='Aegis on tablet'
                    />
                  </Field>
                ) : null}
                {enrollStep === 'scan' && totpScan ? (
                  <>
                    <div
                      id='totp-enrollment-qr'
                      {...stylex.props(styles.enrollQr)}
                      dangerouslySetInnerHTML={{ __html: renderSVG(totpScan.otpauthUri) }}
                    />
                    <Text variant='body-2' color='neutral-faded'>
                      Scan the QR code with your authenticator app, or enter the secret by hand:
                    </Text>
                    <Text variant='body-2' style={styles.enrollSecret}>
                      {totpScan.secret}
                    </Text>
                    <Field id='field-totp-enroll-code'>
                      <FieldLabel htmlFor='totp-enroll-code'>
                        The code your authenticator renders
                      </FieldLabel>
                      <OTPField
                        length={8}
                        value={enrollCode}
                        onValueChange={(next) => setEnrollCode(next.replace(/\D/g, '').slice(0, 8))}
                      >
                        {Array.from({ length: 8 }, (_, slot) => (
                          <OTPFieldSlot key={slot} />
                        ))}
                      </OTPField>
                    </Field>
                  </>
                ) : null}
                {enrollStep === 'codes' && enrollCodes ? (
                  <>
                    <Text variant='body-2' color='neutral-faded'>
                      Ten single-use codes, shown once — the backend keeps hashes only. Save them
                      before leaving the panel.
                    </Text>
                    <div {...stylex.props(styles.codes)} id='totp-enrollment-codes'>
                      {enrollCodes.map((code) => (
                        <Text key={code} variant='body-2'>
                          {code}
                        </Text>
                      ))}
                    </div>
                    <div {...stylex.props(styles.enrollFooter)}>
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => void copyRecoveryCodes(enrollCodes)}
                      >
                        {codesCopied ? <Check size={16} /> : <Copy size={16} />}
                        {codesCopied ? 'Copied' : 'Copy codes'}
                      </Button>
                    </div>
                  </>
                ) : null}
                {enrollError ? (
                  <Alert variant='destructive' id='totp-enrollment-error'>
                    <AlertTitle>Enrollment failed</AlertTitle>
                    <AlertDescription>{enrollError}</AlertDescription>
                  </Alert>
                ) : null}
                <div {...stylex.props(styles.enrollFooter)}>
                  {enrollStep === 'name' ? (
                    <Button
                      variant='primary'
                      disabled={enrollBusy || enrollName.trim().length === 0}
                      onClick={() => void beginTotpEnrollment()}
                    >
                      {enrollBusy ? <Spinner /> : null}
                      Continue
                    </Button>
                  ) : null}
                  {enrollStep === 'scan' ? (
                    <Button
                      variant='primary'
                      disabled={enrollBusy || !/^[0-9]{6,8}$/.test(enrollCode)}
                      onClick={() => void confirmTotpEnrollment()}
                    >
                      {enrollBusy ? <Spinner /> : null}
                      Confirm
                    </Button>
                  ) : null}
                  {enrollStep === 'codes' ? (
                    <Button variant='primary' onClick={finishEnrollment}>
                      I saved my recovery codes
                    </Button>
                  ) : null}
                  <Button variant='outline' disabled={enrollBusy} onClick={cancelEnrollment}>
                    Cancel
                  </Button>
                </div>
              </div>
            ) : null}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Passkeys</CardTitle>
            <CardDescription>
              The discoverable credentials this device and others hold — a sign-in that names no
              password.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {passkeys.isPending ? (
              <TableSkeleton rows={3} />
            ) : passkeyRows.length === 0 ? (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No passkeys</EmptyTitle>
                  <EmptyDescription>
                    This account holds no passkeys yet — add one on this device.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <div {...stylex.props(styles.listStack)}>
                {passkeyRows.map((row) => (
                  <div key={row.id} {...stylex.props(styles.factorRow)}>
                    <Smartphone {...stylex.props(styles.factorIcon)} />
                    <div {...stylex.props(styles.factorMain)}>
                      <Text variant='body-2' weight='medium'>
                        {row.name}
                      </Text>
                      <Text variant='body-2' color='neutral-faded'>
                        {row.lastUsedAt
                          ? `Last used ${formatInstant(row.lastUsedAt && timestampDate(row.lastUsedAt))}`
                          : `Added ${formatInstant(row.createdAt && timestampDate(row.createdAt))}`}
                      </Text>
                    </div>
                    <Button
                      variant='outline'
                      size='sm'
                      disabled={working}
                      onClick={() => {
                        setPasskeyName(row.name)
                        setPasskeyDialog({ kind: 'rename', credentialId: row.id, name: row.name })
                      }}
                    >
                      Rename
                    </Button>
                    <Button
                      variant='outline'
                      size='sm'
                      disabled={working}
                      onClick={() => deletePasskey(row.id)}
                    >
                      Delete
                    </Button>
                  </div>
                ))}
              </div>
            )}
            <div {...stylex.props(styles.toolbarRow, styles.enrollRow)}>
              <Button
                variant='primary'
                disabled={passkeyBusy}
                onClick={() => {
                  setPasskeyName('')
                  setPasskeyDialog({ kind: 'enroll' })
                }}
              >
                {passkeyBusy ? <Spinner /> : <Plus size={16} />}
                Add a passkey
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Recovery codes</CardTitle>
            <CardDescription>
              Ten single-use codes the sign-in accepts when the authenticator is out of reach. The
              set is shown once, at the moment it is minted.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <div {...stylex.props(pageStyles.stack)}>
              <Text variant='body-2' color='neutral-faded'>
                Regenerating retires every code the account holds now.
              </Text>
              <div {...stylex.props(styles.toolbarRow)}>
                <Button
                  variant='outline'
                  disabled={working || confirmedCount === 0}
                  onClick={regenerateCodes}
                >
                  Regenerate codes
                </Button>
                <Button
                  variant='destructive'
                  disabled={working || confirmedCount === 0}
                  onClick={disableMfa}
                >
                  Disable two-factor
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <ReauthenticateDialog
        open={stepUp.dialogOpen}
        onProven={stepUp.onProven}
        onDismissed={stepUp.onDismissed}
      />
      <CodeRequestDialog
        open={factorCode.open}
        title='Second factor required'
        description='Enter a code from a confirmed authenticator — or one recovery code — to prove you hold the factor this action guards.'
        onSubmit={factorCode.submit}
        onCancel={factorCode.cancel}
      />
      <AlertDialog
        open={regeneratedCodes !== null}
        onOpenChange={(next) => {
          if (!next) {
            setRegeneratedCodes(null)
            setCodesCopied(false)
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Your recovery codes — shown once</AlertDialogTitle>
            <AlertDialogDescription>
              Store them somewhere safe. This is the only time they are displayed; the account keeps
              only their hashes.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div id='security-recovery-codes' {...stylex.props(styles.codes)}>
            {(regeneratedCodes ?? []).map((code) => (
              <Text key={code} variant='body-2'>
                {code}
              </Text>
            ))}
          </div>
          <div {...stylex.props(styles.toolbarRow, styles.enrollRow)}>
            <Button
              variant='outline'
              size='sm'
              onClick={() => void copyRecoveryCodes(regeneratedCodes ?? [])}
            >
              {codesCopied ? <Check size={16} /> : <Copy size={16} />}
              {codesCopied ? 'Copied' : 'Copy codes'}
            </Button>
          </div>
          <AlertDialogFooter>
            <AlertDialogAction onClick={() => setRegeneratedCodes(null)}>
              I saved my recovery codes
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={passkeyDialog !== null}
        onOpenChange={(next) => {
          if (!next) setPasskeyDialog(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {passkeyDialog?.kind === 'rename' ? 'Rename this passkey' : 'Add a passkey'}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {passkeyDialog?.kind === 'rename'
                ? 'The name is what this list renders — the credential does not change.'
                : 'The browser will ask for the authenticator once the name is set.'}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <Field id='field-passkey-name'>
            <FieldLabel htmlFor='passkey-name'>
              {passkeyDialog?.kind === 'rename' ? 'New name' : 'Name this passkey'}
            </FieldLabel>
            <Input
              id='passkey-name'
              value={passkeyName}
              onChange={(e) => setPasskeyName(e.target.value)}
              placeholder='Aegis on tablet'
              maxLength={64}
            />
          </Field>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={passkeyBusy || passkeyName.trim().length === 0}
              onClick={() => {
                if (passkeyDialog?.kind === 'rename') void renamePasskey()
                else void enrollPasskey()
              }}
            >
              {passkeyBusy ? <Spinner /> : null}
              {passkeyDialog?.kind === 'rename' ? 'Rename' : 'Continue'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </AccountFrame>
  )
}
