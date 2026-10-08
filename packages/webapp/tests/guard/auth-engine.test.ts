import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { clearTokenCookies, readTokenCookies, readUserCookie } from '#/libraries/guard/auth-cookies'
import { createAuthEngine } from '#/libraries/guard/auth-engine'
import type { TokenBundle } from '#/libraries/guard/auth-engine'
import { authWorker } from '#/libraries/guard/auth-worker-client'
import {
  fetchStub,
  getSessionJson,
  headerOf,
  jsonResponse,
  signInJson,
  stringBody,
  stringUrl,
  tokenJson,
  userJson
} from './auth-connect-mock'

describe('auth engine', () => {
  let fetchMock: ReturnType<typeof fetchStub>

  beforeEach(() => {
    fetchMock = fetchStub()
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('signs in and holds the token pair out of the returned profile', async () => {
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')

    const session = await engine.login({ identity: 'rlangdon', password: 'sophie' })

    expect(session.user).toEqual(userJson)
    expect(session.tokens.sessionId).toBe(tokenJson.sessionId)
    expect(session.user).not.toHaveProperty('accessToken')
    expect(session.user).not.toHaveProperty('refreshToken')
    await expect(engine.accessToken()).resolves.toBe(tokenJson.accessToken)
  })

  it('refuses the multi-factor fork — it ships in a later slice', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse({ ...signInJson, accessToken: '', refreshToken: '', mfaRequired: true })
    )
    const engine = createAuthEngine('http://test.local')

    await expect(engine.login({ identity: 'rlangdon', password: 'sophie' })).rejects.toThrow(
      /multi-factor/i
    )
    await expect(engine.accessToken()).resolves.toBeNull()
  })

  it('carries the remember flag on the wire as the contract names it', async () => {
    fetchMock.mockImplementation(async () => jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')

    await engine.login({ identity: 'rlangdon', password: 'sophie' }, { rememberMe: true })
    const rememberedBody = JSON.parse(stringBody(fetchMock.mock.calls[0]?.[1]?.body))

    await engine.login({ identity: 'rlangdon', password: 'sophie' })
    const plainBody = JSON.parse(stringBody(fetchMock.mock.calls[1]?.[1]?.body))

    expect(rememberedBody).toEqual({ identity: 'rlangdon', password: 'sophie', remember: true })
    expect(plainBody.remember).toBeUndefined()
    expect(stringUrl(fetchMock.mock.calls[0]?.[0])).toContain('/saka.authn.v1.AuthService/SignIn')
  })

  it('rotates the pair through SessionService/Refresh and sends no Bearer', async () => {
    fetchMock.mockImplementation(async () => jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })

    fetchMock.mockResolvedValue(jsonResponse({ ...signInJson, accessToken: 'access-b' }))
    await expect(engine.refresh()).resolves.toBe(true)

    await expect(engine.accessToken()).resolves.toBe('access-b')
    const refreshCall = fetchMock.mock.calls.at(-1)
    expect(stringUrl(refreshCall?.[0])).toContain('/saka.authn.v1.SessionService/Refresh')
    expect(JSON.parse(stringBody(refreshCall?.[1]?.body))).toEqual({ refreshToken: 'refresh-r' })
    expect(headerOf(refreshCall, 'authorization')).toBeNull()
  })

  it('dedupes concurrent refreshes into a single network request', async () => {
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })
    fetchMock.mockClear()
    fetchMock.mockResolvedValue(jsonResponse(signInJson))

    const results = await Promise.all([engine.refresh(), engine.refresh(), engine.refresh()])

    expect(results).toEqual([true, true, true])
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('short-circuits during the cooldown after a failed refresh and drops the pair', async () => {
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })

    fetchMock.mockResolvedValue(
      jsonResponse({ code: 'unauthenticated', message: 'invalid token' }, 401)
    )
    await expect(engine.refresh()).resolves.toBe(false)
    await expect(engine.accessToken()).resolves.toBeNull()
    const callsAfterFailure = fetchMock.mock.calls.length

    await expect(engine.refresh()).resolves.toBe(false)
    expect(fetchMock.mock.calls.length).toBe(callsAfterFailure)
  })

  it('skips the network in maybeRefresh while the access token is still fresh', async () => {
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })
    const callsAfterLogin = fetchMock.mock.calls.length

    await expect(engine.maybeRefresh(5 * 60_000)).resolves.toBe(true)
    expect(fetchMock.mock.calls.length).toBe(callsAfterLogin)
  })

  it('discards an in-flight refresh that settles after logout', async () => {
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })

    let release!: () => void
    const gate = new Promise<void>((resolve) => (release = resolve))
    fetchMock.mockImplementationOnce(() => gate.then(() => jsonResponse(signInJson)))
    fetchMock.mockResolvedValue(jsonResponse({ status: 'success' }))

    const pendingRefresh = engine.refresh()
    await engine.logout()
    release()

    await expect(pendingRefresh).resolves.toBe(false)
  })

  it('spends the access token on sign-out and answers null afterwards', async () => {
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })

    await engine.logout()

    const signOutCall = fetchMock.mock.calls.findLast?.((call) =>
      stringUrl(call[0]).includes('SignOut')
    )
    expect(stringUrl(signOutCall?.[0])).toContain('/saka.authn.v1.SessionService/SignOut')
    expect(headerOf(signOutCall, 'authorization')).toBe(`Bearer ${tokenJson.accessToken}`)
    await expect(engine.accessToken()).resolves.toBeNull()
  })

  it('restores a still-valid pair and refuses a dead one', async () => {
    fetchMock.mockImplementation(async () => jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    const session = await engine.login({ identity: 'rlangdon', password: 'sophie' })

    const freshEngine = createAuthEngine('http://test.local')
    await freshEngine.restore(session.tokens)
    await expect(freshEngine.accessToken()).resolves.toBe(tokenJson.accessToken)

    await freshEngine.restore({ ...session.tokens, refreshExpiresAt: Date.now() - 1000 })
    await expect(freshEngine.accessToken()).resolves.toBeNull()
  })

  it('reports every custody change to the token listener', async () => {
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    const changes: (TokenBundle | null)[] = []
    await engine.setTokenListener((tokens) => changes.push(tokens))

    const session = await engine.login({ identity: 'rlangdon', password: 'sophie' })
    expect(changes).toEqual([session.tokens])

    fetchMock.mockResolvedValue(jsonResponse({ ...signInJson, accessToken: 'access-b' }))
    await expect(engine.refresh()).resolves.toBe(true)
    expect(changes.at(-1)?.accessToken).toBe('access-b')
    expect(changes).toHaveLength(2)

    await engine.logout()
    expect(changes).toEqual([
      session.tokens,
      expect.objectContaining({ accessToken: 'access-b' }),
      null
    ])
  })

  it('rebuilds the profile through SessionService/GetSession', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(signInJson))
    fetchMock.mockResolvedValue(jsonResponse(getSessionJson))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })

    await expect(engine.session()).resolves.toEqual(userJson)
    const call = fetchMock.mock.calls.at(-1)
    expect(stringUrl(call?.[0])).toContain('/saka.authn.v1.SessionService/GetSession')
    expect(headerOf(call, 'authorization')).toBe(`Bearer ${tokenJson.accessToken}`)
  })

  it('refreshes once and retries GetSession when the access token died', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(signInJson))
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ code: 'unauthenticated', message: 'expired' }, 401)
    )
    fetchMock.mockResolvedValueOnce(jsonResponse({ ...signInJson, accessToken: 'access-b' }))
    fetchMock.mockResolvedValue(jsonResponse(getSessionJson))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })

    await expect(engine.session()).resolves.toEqual(userJson)
    const retry = fetchMock.mock.calls.at(-1)
    expect(stringUrl(retry?.[0])).toContain('/saka.authn.v1.SessionService/GetSession')
    expect(headerOf(retry, 'authorization')).toBe('Bearer access-b')
    const refresh = fetchMock.mock.calls.at(-2)
    expect(stringUrl(refresh?.[0])).toContain('/saka.authn.v1.SessionService/Refresh')
  })

  it('answers no session without a network call when no pair is held', async () => {
    const engine = createAuthEngine('http://test.local')

    await expect(engine.session()).resolves.toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('rethrows a GetSession failure other than unauthenticated without spending a refresh', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(signInJson))
    fetchMock.mockResolvedValue(jsonResponse({ code: 'unavailable', message: 'down' }, 503))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })

    await expect(engine.session()).rejects.toThrow(/down|unavailable|HTTP 503/i)
    expect(stringUrl(fetchMock.mock.calls[1]?.[0])).toContain('GetSession')
    expect(fetchMock.mock.calls).toHaveLength(2)
  })

  it('keeps the pair on a network-failed refresh — inconclusive is not a refusal', async () => {
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })

    fetchMock.mockResolvedValue(jsonResponse({ code: 'unavailable', message: 'down' }, 503))
    await expect(engine.refresh()).resolves.toBe(false)
    // The backend never judged the pair, so it stays; the proactive timer
    // retries after the cooldown.
    await expect(engine.accessToken()).resolves.toBe(tokenJson.accessToken)
  })

  it('drops the pair on a refused refresh — a refusal is a dead pair', async () => {
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    await engine.login({ identity: 'rlangdon', password: 'sophie' })

    fetchMock.mockResolvedValue(
      jsonResponse({ code: 'unauthenticated', message: 'invalid token' }, 401)
    )
    await expect(engine.refresh()).resolves.toBe(false)
    await expect(engine.accessToken()).resolves.toBeNull()
  })

  it('reports an accepted restore through the listener', async () => {
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const engine = createAuthEngine('http://test.local')
    const session = await engine.login({ identity: 'rlangdon', password: 'sophie' })

    const changes: (TokenBundle | null)[] = []
    await engine.setTokenListener((tokens) => changes.push(tokens))
    await engine.restore(session.tokens)
    expect(changes).toEqual([session.tokens])

    await engine.restore({ ...session.tokens, refreshExpiresAt: Date.now() - 1000 })
    expect(changes.at(-1)).toBeNull()
  })

  it('mirrors every custody change into the cookie — the reload survival path', async () => {
    clearTokenCookies()
    fetchMock.mockResolvedValue(jsonResponse(signInJson))
    const worker = authWorker()

    // Sign-in writes the fresh pair and caches the profile.
    await expect(worker.login({ identity: 'rlangdon', password: 'sophie' })).resolves.toEqual(
      userJson
    )
    expect(readTokenCookies()?.refreshToken).toBe('refresh-r')
    expect(readUserCookie()).toEqual(userJson)

    // Rotation must rewrite the cookie — the stale copy is what killed reloads.
    fetchMock.mockResolvedValue(
      jsonResponse({ ...signInJson, accessToken: 'access-b', refreshToken: 'refresh-b' })
    )
    await expect(worker.refresh()).resolves.toBe(true)
    expect(readTokenCookies()?.accessToken).toBe('access-b')
    expect(readTokenCookies()?.refreshToken).toBe('refresh-b')
    expect(readUserCookie()).toEqual(userJson)

    // A refused refresh drops the pair and every cookie with it.
    fetchMock.mockResolvedValue(
      jsonResponse({ code: 'unauthenticated', message: 'invalid token' }, 401)
    )
    await expect(worker.refresh()).resolves.toBe(false)
    expect(readTokenCookies()).toBeNull()
    expect(readUserCookie()).toBeNull()
  })

  it('rebuilds the session from the cookie through restore and GetSession', async () => {
    clearTokenCookies()
    fetchMock.mockResolvedValueOnce(jsonResponse(signInJson))
    fetchMock.mockResolvedValue(jsonResponse(getSessionJson))
    const worker = authWorker()

    await worker.login({ identity: 'rlangdon', password: 'sophie' })
    // A reload: the worker's memory is gone, the cookie copy is what remains.
    await worker.restore()
    await expect(worker.session()).resolves.toEqual(userJson)
    expect(readUserCookie()).toEqual(userJson)
  })
})
