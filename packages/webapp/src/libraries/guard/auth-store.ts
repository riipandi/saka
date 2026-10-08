import { createStore } from '@tanstack/react-store'

// ── Types ──────────────────────────────────────────────────────────────────

/**
 * The account view the backend answers (`saka.authn.v1.AuthenticatedUser`),
 * flattened for the UI. The wire form of `id` is the TypeID (`user_…`); the
 * row's UUID never leaves the server.
 */
export interface UserProfile {
  id: string
  username: string
  email: string
  displayName: string
}

export interface AuthState {
  user: UserProfile | null
  /** True while the initial session bootstrap is running. */
  isLoading: boolean
}

// ── Store ──────────────────────────────────────────────────────────────────

/**
 * UI-facing session state — memory only.
 *
 * The live token pair is owned by the auth worker; its cookie copy is
 * JS-readable by construction (the backend answers tokens in the response
 * body, not Set-Cookie), so the exposure window is bounded by rotation: the
 * refresh token dies server-side the moment a renewal lands. On reload the
 * pair is restored from the cookie into the worker (see `guard/auth-session.ts`).
 */
export const authStore = createStore<AuthState>({ user: null, isLoading: false })

// ── Sync reads (for non-React contexts: route guards, API interceptors) ────

/** Returns true when a session is established (user profile present). */
export function isAuthenticated(): boolean {
  return authStore.state.user !== null
}

// ── Store actions ──────────────────────────────────────────────────────────

/** Store the authenticated user profile (memory only). */
export function setAuthUser(user: UserProfile | null) {
  authStore.setState((prev) => ({ ...prev, user }))
}

export function setAuthLoading(isLoading: boolean) {
  authStore.setState((prev) => ({ ...prev, isLoading }))
}

/** Clear the session — used on logout / failed validation. */
export function clearAuth() {
  authStore.setState(() => ({ user: null, isLoading: false }))
}

// ── Sign-out guard (module-level, deliberately not reactive) ────────────────

let signingOut = false

/**
 * True while a user-initiated sign-out is under way. The `(app)` layout's
 * eviction effect watches the profile and redirects on its loss — but a
 * logout sets the profile to null by design, and the eviction's own redirect
 * would race the goodbye redirect the logout performs. The flag is read
 * synchronously by the effect, never rendered from.
 */
export function setSigningOut(value: boolean) {
  signingOut = value
}

export function isSigningOut() {
  return signingOut
}
