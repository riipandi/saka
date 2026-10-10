import { describe, expect, it, vi } from 'vite-plus/test'
import { decodeAccessClaims, emptyGrants } from '#/libraries/guard/auth-claims'

/** Build a compact JWT the way the backend signs one: base64url, no padding. */
function encodeSegment(value: object): string {
  return btoa(JSON.stringify(value)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function jwtWithClaims(claims: Record<string, unknown>): string {
  return `${encodeSegment({ alg: 'ES256', typ: 'JWT' })}.${encodeSegment(claims)}.signature`
}

const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})

describe('access claims decoder', () => {
  it('reads the grant claims off a shaped token', () => {
    const token = jwtWithClaims({
      email: 'robert.langdon@example.com',
      sid: 'sess_v1_zzz',
      roles: ['administrator'],
      permissions: ['user:*:read', 'user:usr_1:ban']
    })

    expect(decodeAccessClaims(token)).toEqual({
      roles: ['administrator'],
      permissions: ['user:*:read', 'user:usr_1:ban']
    })
    expect(warnSpy).not.toHaveBeenCalled()
  })

  it('answers empty sets when the token names no grants (the omit contract)', () => {
    const token = jwtWithClaims({
      email: 'sophie.neveu@example.com',
      sid: 'sess_v1_yyy'
    })

    expect(decodeAccessClaims(token)).toEqual(emptyGrants())
    expect(warnSpy).not.toHaveBeenCalled()
  })

  it('answers empty sets for a token that is not a compact JWT', () => {
    expect(decodeAccessClaims('access-a')).toEqual(emptyGrants())
    expect(decodeAccessClaims('a.b')).toEqual(emptyGrants())
    expect(warnSpy).toHaveBeenCalledTimes(2)
  })

  it('answers empty sets for an unreadable payload', () => {
    expect(decodeAccessClaims('header.%%%not-base64.signature')).toEqual(emptyGrants())
    expect(decodeAccessClaims(`${encodeSegment({ alg: 'ES256' })}.bm90LWpzb24.sig`)).toEqual(
      emptyGrants()
    )
  })

  it('answers empty sets when the claims are the wrong shape', () => {
    const token = jwtWithClaims({ roles: 'administrator', permissions: 'all' })

    expect(decodeAccessClaims(token)).toEqual(emptyGrants())
  })

  it('answers empty sets for a missing token', () => {
    expect(decodeAccessClaims(null)).toEqual(emptyGrants())
    expect(decodeAccessClaims(undefined)).toEqual(emptyGrants())
    expect(decodeAccessClaims('')).toEqual(emptyGrants())
  })

  it('keeps the grant slugs verbatim, including the wildcard', () => {
    const token = jwtWithClaims({
      roles: ['curator', 'viewer'],
      permissions: ['user:*:read', 'widget:wid_1:update', 'group:grp_9:create']
    })

    expect(decodeAccessClaims(token).permissions).toEqual([
      'user:*:read',
      'widget:wid_1:update',
      'group:grp_9:create'
    ])
  })
})
