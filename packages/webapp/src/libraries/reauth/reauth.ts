import { Code, type CallOptions, ConnectError } from '@connectrpc/connect'

/** The header a step-up-guarded procedure reads its proof from. */
export const REAUTHENTICATION_HEADER = 'x-saka-reauthentication'

/**
 * The procedures this SPA calls that the guard's rule table marks
 * step-up-guarded (`internal/guard/rules.go`), mirrored by path. The set is
 * what a call site consults before reaching for the reauthentication modal —
 * a new guarded action joins here with the action that needs it.
 */
export const STEP_UP_PROCEDURES: ReadonlySet<string> = new Set([
  'saka.authn.v1.SessionService/RevokeSession',
  'saka.authn.v1.MultifactorService/RegenerateRecoveryCodes',
  'saka.authn.v1.MultifactorService/DisableMfa',
  'saka.authn.v1.WebAuthnService/DeleteCredential',
  'saka.identity.v1.UserService/AddPassword',
  'saka.identity.v1.UserService/RemovePassword'
])

/** The per-call options that spend one reauthentication proof. */
export function reauthOptions(token: string): CallOptions {
  return { headers: { [REAUTHENTICATION_HEADER]: token } }
}

/**
 * The proof's refusal on the wire. A missing, spent, or foreign proof all
 * answer `unauthenticated` (`internal/transport/middleware/guard.go`) — the
 * same code an expired session answers, so this test is only meaningful for
 * a call the runner just attached a fresh proof to: there it reads as "the
 * proof died between mint and spend", and the answer is a second challenge.
 */
export function isProofRefusal(error: unknown): boolean {
  return error instanceof ConnectError && error.code === Code.Unauthenticated
}

/** The caller dismissed the challenge; the guarded action does not run. */
export class StepUpCancelled extends Error {
  constructor() {
    super('Reauthentication was cancelled.')
    this.name = 'StepUpCancelled'
  }
}

/** The runner a guarded action rides — challenge, spend, retry-once. */
export type StepUpRunner = <T>(call: (options: CallOptions) => Promise<T>) => Promise<T>

/**
 * The runner a guarded action rides: it challenges first (D1 — the modal
 * opens before the call), spends the proof on the one call it was minted
 * for, and when the call answers the proof's refusal it challenges once
 * more and retries — the fresh-proof case, not a loop.
 */
export function createStepUpRunner(challenge: () => Promise<string>): StepUpRunner {
  const run: StepUpRunner = async (call) => {
    const first = await challenge()
    try {
      return await call(reauthOptions(first))
    } catch (error) {
      if (!isProofRefusal(error)) throw error
      return call(reauthOptions(await challenge()))
    }
  }
  return run
}
