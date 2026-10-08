import { afterEach, beforeEach, describe, expect, it } from 'vite-plus/test'
import {
  clearTokenCookies,
  readTokenCookies,
  readUserCookie,
  writeTokenCookies,
  writeUserCookie
} from '#/libraries/guard/auth-cookies'
import type { TokenBundle } from '#/libraries/guard/auth-engine'
import { userJson } from './auth-connect-mock'

const bundle: TokenBundle = {
  accessToken: 'access-a',
  accessExpiresAt: Date.now() + 900_000,
  refreshToken: 'refresh-r',
  refreshExpiresAt: Date.now() + 86400_000,
  sessionId: 'sess_v1_zzz'
}

describe('auth cookies (per-item jar)', () => {
  beforeEach(() => {
    clearTokenCookies()
  })

  afterEach(() => {
    clearTokenCookies()
  })

  it('round-trips the pair as five per-item cookies', () => {
    writeTokenCookies(bundle)
    expect(readTokenCookies()).toEqual(bundle)
    const jar = document.cookie
    expect(jar).toContain('saka_token=access-a')
    expect(jar).toContain('saka_refresh=refresh-r')
    expect(jar).toContain('saka_session_id=sess_v1_zzz')
  })

  it('clears every item, leaving no readable residue', () => {
    writeTokenCookies(bundle)
    writeUserCookie(userJson)
    clearTokenCookies()
    expect(readTokenCookies()).toBeNull()
    expect(readUserCookie()).toBeNull()
  })

  it('treats a missing item as no pair at all', () => {
    writeTokenCookies(bundle)
    // Drop one item — the jar is only whole when all five are present.
    document.cookie = 'saka_session_id=; path=/; max-age=0'
    expect(readTokenCookies()).toBeNull()
  })

  it('answers null — never throws — on a hand-tampered profile cookie', () => {
    document.cookie = 'saka_user={not json at all; path=/'
    expect(readUserCookie()).toBeNull()

    document.cookie = 'saka_user=%7B%22id%22%3A%22%22%7D; path=/'
    expect(readUserCookie()).toBeNull()
  })

  it('round-trips the cached profile', () => {
    writeUserCookie(userJson)
    expect(readUserCookie()).toEqual(userJson)
  })
})
