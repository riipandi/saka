import type { SignInChallenge } from '#/libraries/guard/auth-engine'

/**
 * The pause slot between the sign-in's fork and the multi-factor page. A
 * fork carries no tokens — its bridge (the pending token plus the expiry
 * the mint stamped on it) lands here and the browser goes to `/login/mfa`,
 * so a REFRESH of that page restores the paused state instead of bouncing
 * back to the credentials form. The slot is sessionStorage: it survives a
 * reload and dies with the tab, and the bridge's own `expiresAt` — the
 * backend's expiry, not a local guess — is the only clock the read trusts.
 * The URL still never carries the bridge (plan-20261010_1830 D3).
 */
const PAUSE_KEY = 'saka.sign-in.pause'

export interface SignInPause {
  challenge: SignInChallenge
  returnTo?: string
}

export function pauseSignInChallenge(challenge: SignInChallenge, returnTo?: string): void {
  const pause: SignInPause = { challenge, returnTo }
  sessionStorage.setItem(PAUSE_KEY, JSON.stringify(pause))
}

/** The paused bridge, when one is stored and still live; a read is not a
 * take — the entry stays until the flow consumes it (restart, success) or
 * its expiry does. */
export function readSignInPause(): SignInPause | null {
  const raw = sessionStorage.getItem(PAUSE_KEY)
  if (!raw) return null
  try {
    const pause: unknown = JSON.parse(raw)
    if (typeof pause !== 'object' || pause === null) return null
    const record: Record<string, unknown> = {}
    for (const [key, value] of Object.entries(pause)) record[key] = value
    const challengeRaw = record.challenge
    if (typeof challengeRaw !== 'object' || challengeRaw === null) return null
    const challenge: Record<string, unknown> = {}
    for (const [key, value] of Object.entries(challengeRaw)) challenge[key] = value
    if (typeof challenge.pendingToken !== 'string') return null
    if (typeof challenge.expiresAt !== 'number' || challenge.expiresAt <= Date.now()) return null
    const returnTo = typeof record.returnTo === 'string' ? record.returnTo : undefined
    if (challenge.kind === 'enrollment-required') {
      return {
        challenge: {
          kind: 'enrollment-required',
          pendingToken: challenge.pendingToken,
          expiresAt: challenge.expiresAt
        },
        returnTo
      }
    }
    return {
      challenge: {
        kind: 'mfa-challenge',
        pendingToken: challenge.pendingToken,
        expiresAt: challenge.expiresAt
      },
      returnTo
    }
  } catch {
    return null
  }
}

export function clearSignInPause(): void {
  sessionStorage.removeItem(PAUSE_KEY)
}

/** Whether a pause existed but its bridge died — the expiry notice's fact.
 * A true answer consumes the raw entry, so the notice says it once. */
export function consumeExpiredSignInPause(): boolean {
  const raw = sessionStorage.getItem(PAUSE_KEY)
  if (!raw) return false
  sessionStorage.removeItem(PAUSE_KEY)
  try {
    const pause: unknown = JSON.parse(raw)
    if (typeof pause !== 'object' || pause === null) return false
    const record: Record<string, unknown> = {}
    for (const [key, value] of Object.entries(pause)) record[key] = value
    const challengeRaw = record.challenge
    if (typeof challengeRaw !== 'object' || challengeRaw === null) return false
    const challenge: Record<string, unknown> = {}
    for (const [key, value] of Object.entries(challengeRaw)) challenge[key] = value
    return (
      typeof challenge.pendingToken === 'string' &&
      typeof challenge.expiresAt === 'number' &&
      challenge.expiresAt <= Date.now()
    )
  } catch {
    return false
  }
}
