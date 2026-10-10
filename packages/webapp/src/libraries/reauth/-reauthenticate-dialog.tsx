import { create } from '@bufbuild/protobuf'
import { useMutation } from '@connectrpc/connect-query'
import { startAuthentication } from '@simplewebauthn/browser'
import { useState } from 'react'
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
import { Field, FieldLabel } from 'uilibs/components/base/field'
import { Input } from 'uilibs/components/base/input'
import { Tabs, TabsContent, TabsList, TabsTrigger } from 'uilibs/components/base/tabs'
import { InputPassword } from 'uilibs/components/extra/input-password'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import {
  WebAuthnService,
  BeginLoginRequestSchema,
  PasskeyProofSchema,
  ReauthenticateRequestSchema,
  SendReauthenticationCodeRequestSchema
} from '~/codegen/webauthn_pb'

const proofTabs = ['password', 'passkey', 'email-code'] as const

type ProofKind = (typeof proofTabs)[number]

function proofTab(value: string): ProofKind {
  return proofTabs.find((tab) => tab === value) ?? 'password'
}

/**
 * The step-up modal: the caller proves once again — by the account's
 * password, by a passkey assertion over a fresh `BeginLogin` ceremony, or by
 * the single-use code `SendReauthenticationCode` delivered — and the answer
 * is one reauthentication token, spent by exactly the guarded call that
 * asked (`X-Saka-Reauthentication`). The modal never stores the token: it
 * hands it to `onProven` and forgets.
 */
export function ReauthenticateDialog({
  open,
  onProven,
  onDismissed
}: {
  open: boolean
  onProven: (token: string) => void
  onDismissed: () => void
}) {
  const [proof, setProof] = useState<(typeof proofTabs)[number]>('password')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [codeSent, setCodeSent] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const reauthenticate = useMutation(WebAuthnService.method.reauthenticate, {
    onSuccess: (response) => {
      onProven(response.token)
    },
    onError: (mutationError) => setError(getErrorMessage(mutationError))
  })

  const sendCode = useMutation(WebAuthnService.method.sendReauthenticationCode, {
    onSuccess: () => {
      setCodeSent(true)
      setError(null)
    },
    onError: (mutationError) => setError(getErrorMessage(mutationError))
  })

  const beginLogin = useMutation(WebAuthnService.method.beginLogin)

  // Every opening starts clean: a factor that half-succeeded for a spent
  // proof must not leak its state into the next challenge.
  const reset = () => {
    setPassword('')
    setCode('')
    setCodeSent(false)
    setError(null)
    setProof('password')
  }

  const proveWithPassword = () => {
    setError(null)
    reauthenticate.mutate(
      create(ReauthenticateRequestSchema, { proof: { case: 'password', value: password } })
    )
  }

  const proveWithPasskey = async () => {
    setError(null)
    try {
      const ceremony = await beginLogin.mutateAsync(create(BeginLoginRequestSchema))
      const assertion = await startAuthentication({ optionsJSON: JSON.parse(ceremony.options) })
      reauthenticate.mutate(
        create(ReauthenticateRequestSchema, {
          proof: {
            case: 'passkey',
            value: create(PasskeyProofSchema, {
              sessionId: ceremony.sessionId,
              credential: JSON.stringify(assertion)
            })
          }
        })
      )
    } catch (ceremonyError) {
      setError(getErrorMessage(ceremonyError))
    }
  }

  const proveWithCode = () => {
    setError(null)
    reauthenticate.mutate(
      create(ReauthenticateRequestSchema, { proof: { case: 'emailCode', value: code } })
    )
  }

  const proving = reauthenticate.isPending || beginLogin.isPending || sendCode.isPending

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          reset()
          onDismissed()
        }
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Confirm it is you</AlertDialogTitle>
          <AlertDialogDescription>
            This action touches something a stolen session must not reach. Prove your identity once
            more; the proof is spent on this action alone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <Tabs value={proof} onValueChange={(next) => setProof(proofTab(next))}>
          <TabsList variant='line'>
            {proofTabs.map((tab) => (
              <TabsTrigger key={tab} value={tab}>
                {tab === 'password' ? 'Password' : tab === 'passkey' ? 'Passkey' : 'Email code'}
              </TabsTrigger>
            ))}
          </TabsList>
          <TabsContent value='password'>
            <Field>
              <FieldLabel htmlFor='reauth-password'>Account password</FieldLabel>
              <InputPassword
                id='reauth-password'
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                autoComplete='current-password'
              />
            </Field>
          </TabsContent>
          <TabsContent value='passkey'>
            <Button variant='outline' onClick={() => void proveWithPasskey()}>
              Use a passkey
            </Button>
          </TabsContent>
          <TabsContent value='email-code'>
            {codeSent ? (
              <Field>
                <FieldLabel htmlFor='reauth-code'>The code we emailed you</FieldLabel>
                <Input
                  id='reauth-code'
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  autoComplete='one-time-code'
                />
              </Field>
            ) : (
              <Button
                variant='outline'
                disabled={sendCode.isPending}
                onClick={() => sendCode.mutate(create(SendReauthenticationCodeRequestSchema))}
              >
                Email me a code
              </Button>
            )}
          </TabsContent>
        </Tabs>
        {error ? (
          <Text render={<p />} variant='body-2' color='critical'>
            {error}
          </Text>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={proving || proof === 'passkey' || (proof === 'email-code' && !codeSent)}
            onClick={() => {
              if (proof === 'password') proveWithPassword()
              if (proof === 'email-code') proveWithCode()
            }}
          >
            {proving ? <Spinner /> : null}
            Confirm
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
