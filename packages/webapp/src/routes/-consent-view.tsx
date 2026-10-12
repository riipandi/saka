import * as stylex from '@stylexjs/stylex'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import {
  OTPField,
  OTPFieldGroup,
  OTPFieldSeparator,
  OTPFieldSlot
} from 'uilibs/components/base/otp-field'
import { Alert, AlertDescription, AlertTitle } from 'uilibs/components/extra/alert'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { z } from 'zod'
import { ScopeChip } from '#/components/scope-chip'
import { authFetch } from '#/libraries/api-client'
import { styles } from '#/styles/pages/interaction.stylex'

/**
 * The header that arms the backend's completion dialect: the provider's
 * success is a redirect no fetch can read, so the SPA's calls to the
 * callback paths ask for the target as JSON instead. Keyed by an explicit
 * header, never by the bearer alone — the protocol's other callers keep
 * the raw 302.
 */
export const DIALECT_HEADER = 'X-Saka-Interaction'
export const DIALECT_VALUE = 'spa'

/**
 * The interaction document's shape — the REST JSON the callback answers
 * while the consent question stands. The codegen owns the wire types of
 * `/rpc`; this document is the one answer that travels the raw REST
 * surface, so zod parses it before it reaches the view.
 */
export const interactionDocumentSchema = z.object({
  interaction_id: z.string(),
  client_id: z.string(),
  client_name: z.string(),
  requested_scopes: z.array(z.string()),
  consent_required: z.boolean(),
  login_hint: z.string().default('')
})

export type InteractionDocument = z.infer<typeof interactionDocumentSchema>

export type ResumeAnswer =
  | { kind: 'redirect'; target: string }
  | { kind: 'consent'; document: InteractionDocument }
  | { kind: 'approved' }

const resumeAnswerSchema = z.union([z.object({ redirect: z.string() }), interactionDocumentSchema])

/**
 * The callback the provider redirects with is the resume URL: the session
 * id the SPA carries back. Three shapes exist — the authorization flow's
 * consent continuation, the device flow's code entry, and the device
 * flow's own continuation. Anything else is a not-found, never a redirect
 * target.
 */
const AUTHORIZE_CALLBACK = /^\/oidc\/authorize\/[A-Za-z0-9._-]+$/
const DEVICE_ENTRY_CALLBACK = /^\/oidc\/device$/
const DEVICE_RESUME_CALLBACK = /^\/oidc\/device\/[A-Za-z0-9._-]+$/

export type InteractionCallbackKind = 'authorize' | 'device-entry' | 'device-resume'

export function interactionCallbackKind(value: string | undefined): InteractionCallbackKind | null {
  if (!value || value.length > 512) return null
  if (!value.startsWith('/') || value.startsWith('//') || value.startsWith('/\\')) return null
  if (AUTHORIZE_CALLBACK.test(value)) return 'authorize'
  if (DEVICE_ENTRY_CALLBACK.test(value)) return 'device-entry'
  if (DEVICE_RESUME_CALLBACK.test(value)) return 'device-resume'
  return null
}

export function parseInteractionCallback(value: string | undefined): string | null {
  return interactionCallbackKind(value) === null ? null : (value ?? null)
}

/**
 * The provider's redirects name the resume point as a full interaction
 * URL — `/interaction?callback=%2Foidc%2Fdevice%2Fs…` — the dialect's
 * JSON answers carry the same spelling. The flow must not navigate there
 * (a navigation could not carry the bearer), so the answer's target is
 * unwrapped back into the callback it carries.
 */
export function redirectCallbackTarget(target: string): string | null {
  if (!target.startsWith('/interaction')) return parseInteractionCallback(target)
  const callback = new URLSearchParams(target.slice('/interaction'.length)).get('callback')
  return parseInteractionCallback(callback ?? undefined)
}

/**
 * The resume and the decision ride the same URL — the GET asks the
 * question, the POST answers it — both over the auth-aware seam, so the
 * bearer travels and a 401 refreshes once before it fails.
 */
async function parseResumeAnswer(response: Response): Promise<ResumeAnswer> {
  if (!response.ok) {
    if (response.status === 401) {
      throw new Error('Your session ended while the app was waiting. Sign in again to continue.')
    }
    if (response.status === 404) {
      throw new Error('The authorization request is gone — start again from the app that sent you.')
    }
    throw new Error(
      'The authorization request could not be read. Start again from the app that sent you.'
    )
  }
  // An empty success body is the device flow's confirmation: the answer
  // carried no redirect and no question — the approval simply stands.
  const text = await response.text()
  if (!text.trim()) return { kind: 'approved' }
  let body: unknown
  try {
    body = JSON.parse(text)
  } catch {
    throw new Error('The authorization request answered in a shape the screen cannot read.')
  }
  const parsed = resumeAnswerSchema.safeParse(body)
  if (!parsed.success) {
    throw new Error('The authorization request answered in a shape the screen cannot read.')
  }
  if ('redirect' in parsed.data) {
    return { kind: 'redirect', target: parsed.data.redirect }
  }
  return { kind: 'consent', document: parsed.data }
}

export async function resumeInteraction(callback: string): Promise<ResumeAnswer> {
  const response = await authFetch(callback, {
    headers: { [DIALECT_HEADER]: DIALECT_VALUE, accept: 'application/json' }
  })
  return parseResumeAnswer(response)
}

export async function approveInteraction(
  callback: string,
  scopes: string[]
): Promise<ResumeAnswer> {
  const response = await authFetch(callback, {
    method: 'POST',
    headers: { [DIALECT_HEADER]: DIALECT_VALUE, 'content-type': 'application/json' },
    body: JSON.stringify({ scopes })
  })
  return parseResumeAnswer(response)
}

/**
 * The device flow's verification: the code the device shows, carried to
 * the entry endpoint. The provider looks the code up by its exact spelling
 * — the provider draws eight capital letters with no dash — so the screen
 * normalizes what the reader typed before it sends it.
 */
export function normalizeUserCode(value: string): string {
  return value
    .toUpperCase()
    .replace(/[^A-Z]/g, '')
    .slice(0, 8)
}

export async function verifyUserCode(code: string): Promise<ResumeAnswer> {
  const response = await authFetch(`/oidc/device?user_code=${encodeURIComponent(code)}`, {
    headers: { [DIALECT_HEADER]: DIALECT_VALUE, accept: 'application/json' }
  })
  return parseResumeAnswer(response)
}

/**
 * The device flow's end state — the approval stands, no redirect follows;
 * the device polls its token and continues on its own.
 */
function ApprovedCard({ onLeave }: { onLeave: () => void }) {
  return (
    <Card style={styles.card}>
      <CardHeader>
        <CardTitle>Device authorized</CardTitle>
        <CardDescription>
          The device continues on its own — you can close this window.
        </CardDescription>
      </CardHeader>
      <CardContent {...stylex.props(styles.content)}>
        <Button onClick={onLeave}>Back</Button>
      </CardContent>
    </Card>
  )
}

/**
 * The consent screen: the provider redirected here with the callback the
 * flow resumes at. Three states share the screen — the device entry (the
 * code the device shows), the consent question itself, and the device's
 * approved answer. Signed in, the screen asks the question — the scopes
 * the app wants, worded for the reader — and posts the answer to the same
 * callback; the JSON redirect that comes back hands the browser to the
 * relying party. A decline is a leave: nothing is posted, the pending
 * session expires, and the relying party's polling times out.
 */
export function ConsentView({
  callback,
  onResume = resumeInteraction,
  onApprove = approveInteraction,
  onVerify = verifyUserCode,
  onNavigate = (target: string) => {
    window.location.href = target
  },
  onLeave = () => {
    if (window.history.length > 1) window.history.back()
    else window.location.href = '/overview'
  }
}: {
  callback: string
  onResume?: (callback: string) => Promise<ResumeAnswer>
  onApprove?: (callback: string, scopes: string[]) => Promise<ResumeAnswer>
  onVerify?: (code: string) => Promise<ResumeAnswer>
  onNavigate?: (target: string) => void
  onLeave?: () => void
}) {
  // The callback the flow is at moves under the reader's feet: the device
  // entry swaps itself for the session's resume once the code checks out.
  // The swap stays in place — a navigation could not carry the bearer the
  // resume needs.
  const [active, setActive] = useState(callback)
  const [approved, setApproved] = useState(false)
  useEffect(() => {
    setActive(callback)
    setApproved(false)
  }, [callback])

  const kind = interactionCallbackKind(active)
  const [answerFailed, setAnswerFailed] = useState<string | null>(null)

  // A redirect answer hands the browser over — either to the relying
  // party the flow belongs to, or back to the login screen when the
  // provider bounced the caller for a session it no longer finds.
  const navigated = useRef(false)
  const handOff = (target: string) => {
    if (navigated.current) return
    if (target.startsWith('/interaction')) {
      navigated.current = true
      onNavigate(
        `/login?return_to=${encodeURIComponent(`/interaction?callback=${encodeURIComponent(callback)}`)}`
      )
      return
    }
    navigated.current = true
    onNavigate(target)
  }

  const question = useQuery({
    queryKey: ['interaction', active],
    queryFn: () => onResume(active),
    retry: false,
    staleTime: 0,
    enabled: kind === 'authorize' || kind === 'device-resume'
  })

  useEffect(() => {
    if (question.data?.kind === 'redirect') handOff(question.data.target)
  }, [question.data])

  // The device entry: the code the device shows. A redirect answer back
  // to the entry is the provider re-prompting — the code did not match.
  const [code, setCode] = useState('')
  const [entryFailed, setEntryFailed] = useState<string | null>(null)
  const entry = useMutation({
    mutationFn: (value: string) => onVerify(value),
    onSuccess: (answer) => {
      if (answer.kind === 'redirect') {
        const resumed = redirectCallbackTarget(answer.target)
        if (!resumed) {
          handOff(answer.target)
          return
        }
        if (interactionCallbackKind(resumed) === 'device-entry') {
          setEntryFailed(
            'That code was not recognized — check it against the device and try again.'
          )
          return
        }
        setActive(resumed)
        return
      }
      if (answer.kind === 'consent') {
        // The caller was signed in and the question stands: resume at the
        // session the document names.
        setActive(`/oidc/device/${answer.document.interaction_id}`)
        return
      }
      setApproved(true)
    },
    onError: (error) => setEntryFailed(error.message)
  })

  const approve = useMutation({
    mutationFn: (consent: InteractionDocument) => onApprove(active, consent.requested_scopes),
    onSuccess: (answer) => {
      if (answer.kind === 'approved') {
        // The device flow's confirmation: no redirect follows, the
        // approval simply stands and the device polls its token.
        setApproved(true)
        return
      }
      if (answer.kind === 'redirect') {
        handOff(answer.target)
        return
      }
      // A document answer leaves the question standing.
      setAnswerFailed('The answer did not go through — try again.')
    },
    onError: (error) => setAnswerFailed(error.message)
  })

  if (approved) {
    return (
      <main {...stylex.props(styles.page)}>
        <ApprovedCard onLeave={onLeave} />
      </main>
    )
  }

  if (kind === 'device-entry') {
    const ready = normalizeUserCode(code).length === 8
    return (
      <main {...stylex.props(styles.page)}>
        <Card style={styles.card}>
          <CardHeader>
            <CardTitle>Device sign-in</CardTitle>
            <CardDescription>Type the code shown on the device asking for access.</CardDescription>
          </CardHeader>
          <CardContent {...stylex.props(styles.content)}>
            <form
              onSubmit={(event) => {
                event.preventDefault()
                if (!ready || entry.isPending) return
                setEntryFailed(null)
                entry.mutate(normalizeUserCode(code))
              }}
            >
              <OTPField
                length={8}
                value={code}
                onValueChange={(value: string) => {
                  setCode(normalizeUserCode(value))
                  setEntryFailed(null)
                }}
                validationType='alphanumeric'
                disabled={entry.isPending}
              >
                <OTPFieldGroup>
                  <OTPFieldSlot />
                  <OTPFieldSlot />
                  <OTPFieldSlot />
                  <OTPFieldSlot />
                </OTPFieldGroup>
                <OTPFieldSeparator />
                <OTPFieldGroup>
                  <OTPFieldSlot />
                  <OTPFieldSlot />
                  <OTPFieldSlot />
                  <OTPFieldSlot />
                </OTPFieldGroup>
              </OTPField>
              {entryFailed ? (
                <Text render={<p />} variant='body-2' color='critical'>
                  {entryFailed}
                </Text>
              ) : null}
              <div {...stylex.props(styles.actions)}>
                <Button type='submit' autoFocus disabled={!ready || entry.isPending}>
                  {entry.isPending ? <Spinner /> : 'Continue'}
                </Button>
                <Button
                  type='button'
                  variant='secondary'
                  onClick={onLeave}
                  disabled={entry.isPending}
                >
                  Cancel
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      </main>
    )
  }

  if (question.isPending) {
    return (
      <main {...stylex.props(styles.page)}>
        <Spinner />
      </main>
    )
  }

  if (question.isError) {
    return (
      <main {...stylex.props(styles.page)}>
        <Card style={styles.card}>
          <CardHeader>
            <CardTitle>Sign-in could not continue</CardTitle>
            <CardDescription>{question.error.message}</CardDescription>
          </CardHeader>
          <CardContent {...stylex.props(styles.content)}>
            <Button onClick={onLeave}>Back</Button>
          </CardContent>
        </Card>
      </main>
    )
  }

  if (question.data.kind === 'redirect') {
    return (
      <main {...stylex.props(styles.page)}>
        <Spinner />
      </main>
    )
  }

  if (question.data.kind === 'approved') {
    // The device session was already decided — a revisit lands on the
    // confirmation, not on the question again.
    return (
      <main {...stylex.props(styles.page)}>
        <ApprovedCard onLeave={onLeave} />
      </main>
    )
  }

  const consent = question.data.document
  const persistent = consent.requested_scopes.includes('offline_access')
  const onDevice = kind === 'device-resume'

  return (
    <main {...stylex.props(styles.page)}>
      <Card style={styles.card}>
        <CardHeader>
          <CardTitle>{consent.client_name}</CardTitle>
          <CardDescription>
            {onDevice
              ? 'is asking to read what you approve below; the device continues on its own.'
              : 'is asking to read what you approve below, then it carries you back to itself.'}
          </CardDescription>
        </CardHeader>
        <CardContent {...stylex.props(styles.content)}>
          {consent.login_hint ? (
            <Text render={<p />} variant='body-2' color='neutral-faded'>
              The app asked for the account “{consent.login_hint}” — the session you are signed in
              with decides.
            </Text>
          ) : null}
          <div {...stylex.props(styles.scopes)}>
            {consent.requested_scopes.map((scope) => (
              <ScopeChip key={scope} scope={scope} />
            ))}
          </div>
          {persistent ? (
            <Alert>
              <AlertTitle>Persistent access</AlertTitle>
              <AlertDescription>
                This app keeps its access until you withdraw it — from Account → Authorized apps, or
                by asking the app to sign you out.
              </AlertDescription>
            </Alert>
          ) : null}
          {answerFailed ? (
            <Text render={<p />} variant='body-2' color='critical'>
              {answerFailed}
            </Text>
          ) : null}
          <div {...stylex.props(styles.actions)}>
            <Button autoFocus onClick={() => approve.mutate(consent)} disabled={approve.isPending}>
              {approve.isPending ? <Spinner /> : 'Allow'}
            </Button>
            <Button variant='secondary' onClick={onLeave} disabled={approve.isPending}>
              Cancel
            </Button>
          </div>
        </CardContent>
      </Card>
    </main>
  )
}
