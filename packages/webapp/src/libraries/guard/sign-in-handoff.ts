import type { SignInChallenge } from './auth-engine'

/**
 * The one-slot handoff between the OAuth callback and the login flow. A
 * sign-in that completed on another route but forked at the multi-factor
 * gate offers its bridge here and sends the browser to `/login`; the login
 * view takes it on mount. The slot is the only carrier — the bridge lives in
 * memory alone and never in a URL (see plan-20261010_1830 D3) — and taking
 * it consumes it, so a refresh or a back-navigation cannot replay a live
 * bridge into a stranger's render.
 */
let offered: SignInChallenge | null = null

export function offerSignInChallenge(challenge: SignInChallenge): void {
  offered = challenge
}

export function takeSignInChallenge(): SignInChallenge | null {
  const taken = offered
  offered = null
  return taken
}
