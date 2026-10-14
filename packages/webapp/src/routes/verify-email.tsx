import { create } from '@bufbuild/protobuf'
import { createConnectQueryKey, useMutation } from '@connectrpc/connect-query'
import { CircleCheck, MailCheck } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { OTPField, OTPFieldSlot } from 'uilibs/components/base/otp-field'
import { Card, CardContent, CardDescription } from 'uilibs/components/extra/card'
import { CardHeader, CardTitle } from 'uilibs/components/extra/card'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { styles } from '#/styles/pages/verify-email.stylex'
import { EmailVerificationService, GetCurrentUserRequestSchema } from '~/codegen/identity_pb'
import { UserService, VerifyEmailRequestSchema } from '~/codegen/identity_pb'

export const Route = createFileRoute('/verify-email')({
  component: RouteComponent,
  validateSearch: (search: Record<string, unknown>) => ({
    code: typeof search.code === 'string' ? search.code : undefined
  }),
  staticData: {
    pageTitle: 'Verify email'
  }
})

const CODE_LENGTH = 12

export function RouteComponent() {
  const { code: initialCode } = Route.useSearch()

  const queryClient = useQueryClient()
  const [code, setCode] = useState(initialCode?.slice(0, CODE_LENGTH) ?? '')
  const [result, setResult] = useState<'idle' | 'verified' | 'failed'>('idle')
  const [error, setError] = useState<string | null>(null)

  const verify = useMutation(EmailVerificationService.method.verifyEmail, {
    onSuccess: () => {
      setError(null)
      setResult('verified')
      // A signed-in reader of the same browser gets the fresh badge without
      // a reload; a signed-out one carries no query to invalidate.
      queryClient.invalidateQueries({
        queryKey: createConnectQueryKey({
          schema: UserService.method.getCurrentUser,
          input: create(GetCurrentUserRequestSchema),
          cardinality: 'finite'
        })
      })
    },
    onError: (verifyError) => {
      setResult('failed')
      setError(getErrorMessage(verifyError))
    }
  })

  if (result === 'verified') {
    return (
      <main {...stylex.props(styles.page)}>
        <Card style={styles.card}>
          <CardHeader>
            <CardTitle>Email verified</CardTitle>
            <CardDescription>The address on record is confirmed.</CardDescription>
          </CardHeader>
          <CardContent {...stylex.props(styles.content)}>
            <CircleCheck size={32} style={styles.icon} />
            <Text render={<p />} variant='body-2' color='neutral-faded'>
              The code is spent — verification cannot run twice.
            </Text>
            <Button render={<a href='/login' />}>Continue to sign in</Button>
          </CardContent>
        </Card>
      </main>
    )
  }

  return (
    <main {...stylex.props(styles.page)}>
      <Card style={styles.card}>
        <CardHeader>
          <CardTitle>Verify your email</CardTitle>
          <CardDescription>
            Type the code from the message — it works once, then it is spent.
          </CardDescription>
        </CardHeader>
        <CardContent {...stylex.props(styles.content)}>
          <OTPField
            length={CODE_LENGTH}
            value={code}
            onValueChange={setCode}
            validationType='alphanumeric'
          >
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
          <Button
            disabled={code.trim().length !== CODE_LENGTH || verify.isPending}
            onClick={() => verify.mutate(create(VerifyEmailRequestSchema, { token: code.trim() }))}
          >
            {verify.isPending ? <Spinner /> : <MailCheck size={16} />}
            Verify
          </Button>
          {error ? (
            <Text render={<p />} variant='body-2' color='critical'>
              {error}
            </Text>
          ) : null}
        </CardContent>
      </Card>
    </main>
  )
}
