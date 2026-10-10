import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { authStore, clearAuth } from '#/libraries/guard/auth-store'
import { authWorker } from '#/libraries/guard/auth-worker-client'
import { jsonResponse, tokenJson, userJson } from './auth-connect-mock'

/**
 * The custody wiring drives the real listener the engine reports to. Unit
 * tests take the main-thread fallback (`authWorker` refuses workers in test
 * mode), and the same listener is what the worker path wires — the wiring
 * under test is the wiring that ships.
 */

/** Build a compact JWT the way the backend signs one: base64url, no padding. */
function encodeSegment(value: object): string {
  return btoa(JSON.stringify(value)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function jwtWithClaims(claims: Record<string, unknown>): string {
  return `${encodeSegment({ alg: 'ES256', typ: 'JWT' })}.${encodeSegment(claims)}.signature`
}

const adminToken = jwtWithClaims({
  sid: 'sess_v1_zzz',
  roles: ['administrator'],
  permissions: ['user:*:read', 'session:sess_x:end']
})
const plainToken = jwtWithClaims({ sid: 'sess_v1_yyy' })

describe('grant custody through the auth client', () => {
  let fetchMock: ReturnType<typeof vi.fn<typeof fetch>>

  beforeEach(() => {
    clearAuth()
    fetchMock = vi.fn<typeof fetch>()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('sign-in decodes the claims the fresh pair carries', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ ...tokenJson, accessToken: adminToken, user: userJson, status: 'success' })
    )
    const client = authWorker()

    await client.login({ identity: 'rlangdon', password: 'sophie' })

    expect(authStore.state.grants).toEqual({
      roles: ['administrator'],
      permissions: ['user:*:read', 'session:sess_x:end']
    })
  })

  it('a claim-less pair answers the empty snapshot, not a stale one', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ ...tokenJson, accessToken: plainToken, user: userJson, status: 'success' })
    )
    const client = authWorker()

    await client.login({ identity: 'rlangdon', password: 'sophie' })

    expect(authStore.state.grants).toEqual({ roles: [], permissions: [] })
  })

  it('refresh replaces the snapshot the new pair carries', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ ...tokenJson, accessToken: plainToken, user: userJson, status: 'success' })
    )
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ ...tokenJson, accessToken: adminToken, status: 'success' })
    )
    const client = authWorker()
    await client.login({ identity: 'rlangdon', password: 'sophie' })

    await client.refresh()

    expect(authStore.state.grants).toEqual({
      roles: ['administrator'],
      permissions: ['user:*:read', 'session:sess_x:end']
    })
  })

  it('sign-out clears the grants beside the profile', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ ...tokenJson, accessToken: adminToken, user: userJson, status: 'success' })
    )
    fetchMock.mockResolvedValue(jsonResponse({}, 200))
    const client = authWorker()
    await client.login({ identity: 'rlangdon', password: 'sophie' })

    await client.logout()

    expect(authStore.state.user).toBeNull()
    expect(authStore.state.grants).toEqual({ roles: [], permissions: [] })
  })
})
