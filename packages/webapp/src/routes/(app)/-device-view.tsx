import { create } from '@bufbuild/protobuf'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import { createClient } from '@connectrpc/connect'
import { createConnectQueryKey, useTransport } from '@connectrpc/connect-query'
import { SmartphoneCheck } from '@keyline-icons/react'
import * as stylex from '@stylexjs/stylex'
import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Button } from 'uilibs/components/base/button'
import {
  OTPField,
  OTPFieldGroup,
  OTPFieldSeparator,
  OTPFieldSlot
} from 'uilibs/components/base/otp-field'
import { Badge } from 'uilibs/components/extra/badge'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from 'uilibs/components/extra/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from 'uilibs/components/extra/empty'
import { Spinner } from 'uilibs/components/extra/spinner'
import { Text } from 'uilibs/components/extra/text'
import { unit } from 'uilibs/styles/tokens.stylex'
import { getErrorMessage } from '#/libraries/guard/auth-utils'
import { pageStyles } from '#/styles/pages/page.stylex'
import {
  DecideDeviceLoginRequestSchema,
  DeviceApprovalService,
  InspectDeviceLoginRequestSchema,
  type InspectDeviceLoginResponse
} from '~/codegen/authn_pb'

const CODE_LENGTH = 8

/** `12345678` renders as `1234-5678`; a typed hyphen folds away — the
 * server's normalization accepts either shape, the display keeps one. */
function formatCode(raw: string): string {
  const digits = raw.replaceAll('-', '').slice(0, CODE_LENGTH)
  return digits.length > 4 ? `${digits.slice(0, 4)}-${digits.slice(4)}` : digits
}

const instantFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short' })

function formatExpiry(value: Date | undefined): string {
  if (!value) return '—'
  return Number.isNaN(value.getTime()) ? '—' : instantFormat.format(value)
}

function createInspectInput(code: string) {
  return create(InspectDeviceLoginRequestSchema, { code })
}

function createDecideInput(code: string, decision: 'approve' | 'deny') {
  return create(DecideDeviceLoginRequestSchema, { code, decision })
}

/**
 * The device approval screen: the signed-in account holder reads what a
 * pairing request names — the code's display form, the requesting browser's
 * address and agent, the instant the request dies — and stamps approve or
 * deny. An unknown, expired, or already-decided code answers not_found on
 * both procedures; the screen keeps the input mounted so a mistyped code is
 * a correction, not a restart.
 */
export function DeviceApprovalView({ initialCode }: { initialCode?: string }) {
  const transport = useTransport()
  const queryClient = useQueryClient()

  const deviceRpc = useMemo(() => createClient(DeviceApprovalService, transport), [transport])

  const [code, setCode] = useState(() => formatCode(initialCode ?? ''))
  const [inspection, setInspection] = useState<InspectDeviceLoginResponse | null>(null)
  const [inspecting, setInspecting] = useState(false)
  const [deciding, setDeciding] = useState<'approve' | 'deny' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notFound, setNotFound] = useState(false)

  // A deep link with ?code=XXXX-XXXX inspects on arrival; a hand-typed code
  // waits for the button. The link's code may already be decided — the
  // not-found surface covers that too.
  useEffect(() => {
    if (!initialCode) return
    const normalized = formatCode(initialCode)
    if (normalized.length !== CODE_LENGTH) return
    void runInspect(normalized)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- the inspection is a one-shot on the deep link's code
  }, [initialCode])

  const runInspect = useCallback(
    async (value: string) => {
      setError(null)
      setNotFound(false)
      setInspection(null)
      setInspecting(true)
      try {
        const answer = await deviceRpc.inspect(createInspectInput(value))
        setInspection(answer)
      } catch (inspectError) {
        // The contract's refusal for an unknown, expired, or decided code
        // is not_found — the screen says so without saying which.
        if (getErrorMessage(inspectError).toLowerCase().includes('not found')) {
          setNotFound(true)
        } else {
          setError(getErrorMessage(inspectError))
        }
      } finally {
        setInspecting(false)
      }
    },
    [deviceRpc]
  )

  const decide = (decision: 'approve' | 'deny') => {
    if (!inspection) return
    setDeciding(decision)
    setError(null)
    deviceRpc
      .decide(createDecideInput(code, decision))
      .then(() => {
        setInspection(null)
        setCode('')
        return queryClient.invalidateQueries({
          queryKey: createConnectQueryKey({
            schema: DeviceApprovalService.method.inspect,
            input: createInspectInput(code),
            cardinality: 'finite'
          })
        })
      })
      .catch((decideError: unknown) => {
        // A decided request is the not-found on re-decision; fold it back
        // to the input so the visitor can start over with a fresh code.
        if (getErrorMessage(decideError).toLowerCase().includes('not found')) {
          setNotFound(true)
          setInspection(null)
          setCode('')
        } else {
          setError(getErrorMessage(decideError))
        }
      })
      .finally(() => setDeciding(null))
  }

  const ready = code.replaceAll('-', '').length === CODE_LENGTH

  return (
    <div
      {...stylex.props(
        pageStyles.container,
        pageStyles.containerPadMedium,
        pageStyles.containerPadLarge,
        pageStyles.containerPadXLarge,
        styles.shell
      )}
    >
      <div {...stylex.props(styles.narrow)}>
        <div {...stylex.props(pageStyles.header)}>
          <div {...stylex.props(pageStyles.headerLeft)}>
            <Text
              render={<p />}
              variant='caption-1'
              weight='semibold'
              color='primary'
              style={pageStyles.kicker}
            >
              Device login
            </Text>
            <Text render={<h1 />} variant='featured-4' weight='bold'>
              Approve a device
            </Text>
            <Text variant='body-2' color='neutral-faded'>
              A browser that cannot sign itself in shows an eight-character code. Enter it here to
              pair that device with this account.
            </Text>
          </div>
        </div>

        <div {...stylex.props(pageStyles.stack)}>
          <Card>
            <CardHeader>
              <CardTitle>Enter the code</CardTitle>
              <CardDescription>The code the requesting device displays, XXXX-XXXX.</CardDescription>
            </CardHeader>
            <CardContent>
              <div {...stylex.props(styles.inputStack)}>
                <OTPField
                  length={CODE_LENGTH}
                  value={code.replaceAll('-', '')}
                  onValueChange={(value) => {
                    setCode(formatCode(value))
                    setNotFound(false)
                    setInspection(null)
                    setError(null)
                  }}
                  disabled={inspecting || deciding !== null}
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
                <div {...stylex.props(styles.actions)}>
                  <Button
                    variant='primary'
                    disabled={!ready || inspecting || deciding !== null}
                    onClick={() => void runInspect(code)}
                  >
                    {inspecting ? <Spinner /> : null}
                    Inspect
                  </Button>
                </div>
              </div>
            </CardContent>
          </Card>

          {notFound ? (
            <Card>
              <CardContent>
                <Empty>
                  <EmptyHeader>
                    <EmptyTitle>No pairing request answers this code</EmptyTitle>
                    <EmptyDescription>
                      The code is unknown, already used, or has expired. Check the code on the
                      requesting device and try again.
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              </CardContent>
            </Card>
          ) : null}

          {inspection ? (
            <InspectionCard inspection={inspection} deciding={deciding} onDecide={decide} />
          ) : null}

          {error ? (
            <Text render={<p />} variant='body-2' color='critical'>
              {error}
            </Text>
          ) : null}
        </div>
      </div>
    </div>
  )
}

function InspectionCard({
  inspection,
  deciding,
  onDecide
}: {
  inspection: InspectDeviceLoginResponse
  deciding: 'approve' | 'deny' | null
  onDecide: (decision: 'approve' | 'deny') => void
}) {
  const expiresAt = inspection.expiresAt ? timestampDate(inspection.expiresAt) : undefined
  const expired = expiresAt ? expiresAt.getTime() <= Date.now() : false
  const handleDecide = (decision: 'approve' | 'deny') => {
    onDecide(decision)
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Requesting device</CardTitle>
        <CardDescription>
          Confirm the facts below match the device asking to sign in before you answer.
        </CardDescription>
        <CardAction>
          <Badge variant={expired ? 'destructive' : 'secondary'}>
            {expired ? 'Expired' : `Expires ${formatExpiry(expiresAt)}`}
          </Badge>
        </CardAction>
      </CardHeader>
      <CardContent>
        <div {...stylex.props(styles.facts)}>
          <FactRow label='Code' value={inspection.userCode} mono />
          <FactRow label='IP address' value={inspection.ipAddress || '—'} mono />
          <FactRow label='Browser' value={inspection.userAgent || '—'} />
        </div>
        <div {...stylex.props(styles.actions)}>
          <Button
            variant='outline'
            disabled={deciding !== null || expired}
            onClick={() => handleDecide('deny')}
          >
            {deciding === 'deny' ? <Spinner /> : null}
            Deny
          </Button>
          <Button
            variant='primary'
            disabled={deciding !== null || expired}
            onClick={() => handleDecide('approve')}
          >
            {deciding === 'approve' ? <Spinner /> : <SmartphoneCheck size={16} />}
            Approve
          </Button>
        </div>
        <Text variant='body-2' color='neutral-faded'>
          Approving signs the requesting device in as this account. Denying ends the pairing attempt
          — the device learns only that the answer was no.
        </Text>
      </CardContent>
    </Card>
  )
}

function FactRow({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div {...stylex.props(styles.factRow)}>
      <Text variant='body-2' color='neutral-faded' style={styles.factLabel}>
        {label}
      </Text>
      <Text render={<p />} variant='body-1' style={mono ? styles.factMono : undefined}>
        {value}
      </Text>
    </div>
  )
}

const styles = stylex.create({
  shell: {
    display: 'flex',
    justifyContent: 'center'
  },
  narrow: {
    width: '100%',
    maxWidth: 640
  },
  inputStack: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    gap: unit.x5
  },
  actions: {
    display: 'flex',
    gap: unit.x3,
    justifyContent: 'flex-end'
  },
  facts: {
    display: 'flex',
    flexDirection: 'column',
    gap: unit.x3,
    marginBottom: unit.x5
  },
  factRow: {
    display: 'flex',
    alignItems: 'baseline',
    gap: unit.x3
  },
  factLabel: {
    width: 96,
    flexShrink: 0
  },
  factMono: {
    fontFamily: 'monospace',
    wordBreak: 'break-all'
  }
})
