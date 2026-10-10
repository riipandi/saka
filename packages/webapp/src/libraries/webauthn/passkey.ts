import { create } from '@bufbuild/protobuf'
import { createClient, type Transport } from '@connectrpc/connect'
import { startAuthentication } from '@simplewebauthn/browser'
import { BeginLoginRequestSchema, WebAuthnService } from '~/codegen/webauthn_pb'

/** The wire's answer: the ceremony handle and the assertion JSON the verify
 * call passes through untouched. */
export interface PasskeyAssertion {
  sessionId: string
  credential: string
}

/**
 * Run the discoverable assertion ceremony: `BeginLogin` answers the
 * browser's `get()` argument, the browser answers the authenticator, and
 * the credential JSON rides to whichever verify call judges it — the
 * sign-in's, the challenge's, or the step-up's. The three share the one
 * ceremony; the account is whoever the credential names.
 */
export async function runPasskeyAssertion(transport: Transport): Promise<PasskeyAssertion> {
  const webauthn = createClient(WebAuthnService, transport)
  const ceremony = await webauthn.beginLogin(create(BeginLoginRequestSchema))
  const assertion = await startAuthentication({ optionsJSON: JSON.parse(ceremony.options) })
  return { sessionId: ceremony.sessionId, credential: JSON.stringify(assertion) }
}
