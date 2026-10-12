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
    const pause = JSON.parse(raw) as SignInPause
    if (!pause.challenge?.pendingToken) return null
    if (pause.challenge.expiresAt <= Date.now()) return null
    return pause
  } catch {
    return null
  }
}

export function clearSignInPause(): void {
  sessionStorage.removeItem(PAUSE_KEY)
}
