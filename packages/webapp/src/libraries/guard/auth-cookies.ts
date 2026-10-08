/**
 * Main-thread persistence for the token pair the auth worker holds. Workers
 * have no cookie access, so the engine reports every custody change through
 * its token listener and this module mirrors it into per-item cookies; at
 * boot `restore()` reads them back.
 *
 * The listener is the only write path — the cookies cannot lag the worker's
 * live pair. JS-written cookies are always JS-readable, so the pair is
 * short-lived and rotated on every renewal: the refresh token dies
 * server-side the moment a rotation lands.
 *
 * Every read and write rides the cookies library's shared instance.
 */
import { z } from 'zod'
import { cookies } from '#/libraries/cookies'
import type { TokenBundle } from './auth-engine'
import type { UserProfile } from './auth-store'

const TOKEN_COOKIE = 'saka_token'
const TOKEN_EXP_COOKIE = 'saka_token_exp'
const REFRESH_COOKIE = 'saka_refresh'
const REFRESH_EXP_COOKIE = 'saka_refresh_exp'
const SESSION_ID_COOKIE = 'saka_session_id'
const USER_COOKIE = 'saka_user'

/**
 * Session posture shared by every write: SameSite=Lax keeps the pair on
 * same-site navigations; Secure whenever the page itself is served over TLS.
 * The shared instance carries `path: '/'`, so every item is site-wide.
 */
function sessionOptions(expiresAtMs?: number) {
  const secure = typeof location !== 'undefined' && location.protocol === 'https:'
  return {
    sameSite: 'lax',
    ...(secure ? { secure: true } : {}),
    ...(expiresAtMs === undefined ? {} : { maxAge: maxAgeSeconds(expiresAtMs) })
  } as const
}

/** Remaining lifetime in whole seconds, floored at zero. */
function maxAgeSeconds(expiresAtMs: number): number {
  return Math.max(0, Math.floor((expiresAtMs - Date.now()) / 1000))
}

/** Persist the pair — each cookie lives exactly as long as the item it holds. */
export function writeTokenCookies(tokens: TokenBundle): void {
  cookies.set(TOKEN_COOKIE, tokens.accessToken, sessionOptions(tokens.accessExpiresAt))
  cookies.set(
    TOKEN_EXP_COOKIE,
    String(tokens.accessExpiresAt),
    sessionOptions(tokens.accessExpiresAt)
  )
  cookies.set(REFRESH_COOKIE, tokens.refreshToken, sessionOptions(tokens.refreshExpiresAt))
  cookies.set(
    REFRESH_EXP_COOKIE,
    String(tokens.refreshExpiresAt),
    sessionOptions(tokens.refreshExpiresAt)
  )
  // The session id is the refresh token's storage key server-side — it lives
  // with the refresh half.
  cookies.set(SESSION_ID_COOKIE, tokens.sessionId, sessionOptions(tokens.refreshExpiresAt))
}

/** Clear every cookie — logout and failed refreshes leave no stale credentials. */
export function clearTokenCookies(): void {
  for (const name of [
    TOKEN_COOKIE,
    TOKEN_EXP_COOKIE,
    REFRESH_COOKIE,
    REFRESH_EXP_COOKIE,
    SESSION_ID_COOKIE,
    USER_COOKIE
  ]) {
    cookies.remove(name)
  }
}

/** The wire shape of a token pair — shared by the cookie jar and tab sync. */
export const tokenBundleSchema = z.object({
  accessToken: z.string().min(1),
  refreshToken: z.string().min(1),
  accessExpiresAt: z.number(),
  refreshExpiresAt: z.number(),
  sessionId: z.string().min(1)
})

/** The raw jar shape — all five items, non-empty, still as strings. */
const jarSchema = z.object({
  [TOKEN_COOKIE]: z.string().min(1),
  [TOKEN_EXP_COOKIE]: z.coerce.number().finite(),
  [REFRESH_COOKIE]: z.string().min(1),
  [REFRESH_EXP_COOKIE]: z.coerce.number().finite(),
  [SESSION_ID_COOKIE]: z.string().min(1)
})

/** Read the persisted pair, or null when any item is absent or malformed. */
export function readTokenCookies(): TokenBundle | null {
  const parsed = jarSchema.safeParse(cookies.getAll({ doNotParse: true }))
  if (!parsed.success) return null
  const jar = parsed.data
  return {
    accessToken: jar[TOKEN_COOKIE],
    accessExpiresAt: jar[TOKEN_EXP_COOKIE],
    refreshToken: jar[REFRESH_COOKIE],
    refreshExpiresAt: jar[REFRESH_EXP_COOKIE],
    sessionId: jar[SESSION_ID_COOKIE]
  }
}

/** The cached profile shape — the store's own view, no wire fields. */
const userSchema = z.object({
  id: z.string().min(1),
  username: z.string().min(1),
  email: z.string().min(1),
  displayName: z.string()
})

/**
 * Cache the profile beside the pair — the reload reads it before any network.
 * The cookie lives as long as the refresh half when its expiry is known
 * (a persistent session survives a browser restart with it); without one it
 * is a session cookie, and the bootstrap falls back to the blocking path.
 */
export function writeUserCookie(user: UserProfile, expiresAtMs?: number): void {
  cookies.set(USER_COOKIE, user, sessionOptions(expiresAtMs))
}

/**
 * Read the cached profile, or null when absent, malformed, or empty — a
 * cleared cookie leaves the name behind with an empty value in some engines,
 * and a hand-tampered value must never throw at boot.
 */
export function readUserCookie(): UserProfile | null {
  const raw = cookies.get(USER_COOKIE, { doNotParse: true })
  if (typeof raw !== 'string' || !raw) return null
  let value: unknown
  try {
    value = JSON.parse(raw)
  } catch {
    return null
  }
  const parsed = userSchema.safeParse(value)
  return parsed.success ? parsed.data : null
}
