/**
 * Main-thread cookie persistence for the token pair the auth worker holds.
 *
 * Workers have no `document.cookie` access, so the engine reports every
 * custody change (sign-in, rotation, failure, sign-out) through its token
 * listener and this module mirrors the pair into per-item cookies; at boot
 * the cookies are read back and handed to the worker via `restore()`. The
 * listener is the only write path — the cookies can never lag behind the
 * worker's live pair, which is what killed the previous wiring (a rotated
 * pair left the stored copy holding the spent refresh token, and the reload
 * replayed it).
 *
 * One cookie per item — the token, its expiry, the refresh token, its
 * expiry, and the session id — so each piece is readable and clearable on
 * its own terms. JS-written cookies are readable by JS — HttpOnly is
 * impossible without the backend setting them — so the pair is short-lived
 * and rotated on every renewal: the refresh token dies server-side the
 * moment a rotation lands.
 */
import { parse, serialize } from 'cookie-es'
import { z } from 'zod'
import type { TokenBundle } from './auth-engine'
import type { UserProfile } from './auth-store'

const TOKEN_COOKIE = 'saka_token'
const TOKEN_EXP_COOKIE = 'saka_token_exp'
const REFRESH_COOKIE = 'saka_refresh'
const REFRESH_EXP_COOKIE = 'saka_refresh_exp'
const SESSION_ID_COOKIE = 'saka_session_id'
const USER_COOKIE = 'saka_user'

/**
 * Cookie options shared by every write and the clear — one definition, no drift.
 * SameSite=Lax keeps the pair on same-site navigations;
 * Secure whenever the page itself is served over TLS.
 */
function cookieOptions(maxAgeSeconds?: number) {
  const secure = typeof location !== 'undefined' && location.protocol === 'https:'
  return {
    path: '/',
    sameSite: 'lax',
    ...(secure ? { secure: true } : {}),
    ...(maxAgeSeconds === undefined ? {} : { maxAge: maxAgeSeconds })
  } as const
}

/** Write one cookie with its own remaining lifetime, in whole seconds. */
function writeCookie(name: string, value: string, expiresAtMs: number): void {
  const maxAge = Math.max(0, Math.floor((expiresAtMs - Date.now()) / 1000))
  document.cookie = serialize(name, value, cookieOptions(maxAge))
}

/** Persist the pair — each cookie lives exactly as long as the item it holds. */
export function writeTokenCookies(tokens: TokenBundle): void {
  writeCookie(TOKEN_COOKIE, tokens.accessToken, tokens.accessExpiresAt)
  writeCookie(TOKEN_EXP_COOKIE, String(tokens.accessExpiresAt), tokens.accessExpiresAt)
  writeCookie(REFRESH_COOKIE, tokens.refreshToken, tokens.refreshExpiresAt)
  writeCookie(REFRESH_EXP_COOKIE, String(tokens.refreshExpiresAt), tokens.refreshExpiresAt)
  // The session id is the refresh token's storage key server-side — it lives
  // with the refresh half.
  writeCookie(SESSION_ID_COOKIE, tokens.sessionId, tokens.refreshExpiresAt)
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
    document.cookie = serialize(name, '', cookieOptions(0))
  }
}

/** The raw cookie jar shape — all five items, non-empty, still as strings. */
const jarSchema = z.object({
  [TOKEN_COOKIE]: z.string().min(1),
  [TOKEN_EXP_COOKIE]: z.coerce.number().finite(),
  [REFRESH_COOKIE]: z.string().min(1),
  [REFRESH_EXP_COOKIE]: z.coerce.number().finite(),
  [SESSION_ID_COOKIE]: z.string().min(1)
})

/** Read the persisted pair, or null when any item is absent or malformed. */
export function readTokenCookies(): TokenBundle | null {
  const parsed = jarSchema.safeParse(parse(document.cookie))
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
  const options =
    expiresAtMs === undefined
      ? cookieOptions()
      : cookieOptions(Math.max(0, Math.floor((expiresAtMs - Date.now()) / 1000)))
  document.cookie = serialize(USER_COOKIE, JSON.stringify(user), options)
}

/**
 * Read the cached profile, or null when absent, malformed, or empty — a
 * cleared cookie leaves the name behind with an empty value in some engines.
 */
export function readUserCookie(): UserProfile | null {
  const raw = parse(document.cookie)[USER_COOKIE]
  if (!raw) return null
  const parsed = userSchema.safeParse(JSON.parse(raw))
  return parsed.success ? parsed.data : null
}
