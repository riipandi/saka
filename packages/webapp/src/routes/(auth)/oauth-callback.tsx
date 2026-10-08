import * as stylex from '@stylexjs/stylex'
import { createFileRoute, Link, useSearch } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import { Alert, AlertDescription, AlertTitle } from 'uilibs/components/extra/alert'
import { Card, CardContent, CardDescription, CardHeader } from 'uilibs/components/extra/card'
import { LoaderText } from 'uilibs/components/extra/loader-text'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { z } from 'zod'
import { useAuthentication } from '#/hooks/use-auth'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { styles } from '#/styles/pages/login.stylex'

export const Route = createFileRoute('/(auth)/oauth-callback')({
  component: RouteComponent,
  validateSearch: z.object({
    flow_token: z.string().optional(),
    error: z.string().optional()
  }),
  staticData: {
    pageTitle: 'Completing sign in'
  }
})

// The codes the flow's redirects carry — the browser is mid-redirect, so
// the words are the whole answer. Rendered with one call to action: start
// again.
export const OAUTH_FLOW_ERROR_MESSAGES: Record<string, string> = {
  unknown_flow: 'This sign-in link has expired or was already used.',
  provider_error: 'The provider refused the sign-in.',
  internal_error: 'The sign-in could not be completed.'
}

/**
 * The OAuth flow's landing: the provider's callback redirected here with
 * either a flow token the sign-in completes from, or an error code. The
 * completion is a one-shot — a spent token cannot be replayed, so the
 * effect starts it once per mount no matter how the render cycles run.
 */
function RouteComponent() {
  const { flow_token: flowToken, error } = useSearch({ from: Route.id })
  const { continueSignIn } = useAuthentication()
  const [failed, setFailed] = useState<string | null>(null)
  const started = useRef(false)

  useEffect(() => {
    if (!flowToken || started.current) return
    started.current = true
    continueSignIn(flowToken).catch((cause: unknown) => setFailed(getErrorMessage(cause)))
  }, [flowToken, continueSignIn])

  const message = error
    ? (OAUTH_FLOW_ERROR_MESSAGES[error] ?? 'The sign-in could not be completed.')
    : null

  return (
    <div {...stylex.props(styles.page)}>
      <Card size='md' id='oauth-callback-card' style={styles.cardRoot}>
        <CardHeader style={styles.header}>
          <Text render={<h1 />} variant='featured-5' weight='semibold'>
            Completing sign in
          </Text>
          <CardDescription>Hold on while your sign-in is finished.</CardDescription>
        </CardHeader>

        <CardContent>
          {message && (
            <Alert variant='destructive' id='oauth-callback-error'>
              <AlertTitle>Sign in failed</AlertTitle>
              <AlertDescription>{message}</AlertDescription>
            </Alert>
          )}
          {failed && (
            <Alert variant='destructive' id='oauth-callback-failed'>
              <AlertTitle>Sign in failed</AlertTitle>
              <AlertDescription>{failed}</AlertDescription>
            </Alert>
          )}
          {flowToken && !failed && (
            <Text variant='body-2'>
              <Spinner />
              <LoaderText variant='body-2'>Finishing your sign-in…</LoaderText>
            </Text>
          )}
          {!flowToken && (
            <Button render={<Link to='/login' />} variant='outline'>
              Back to sign in
            </Button>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
