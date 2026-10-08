import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { clearTokenCookies, writeUserCookie } from '#/libraries/guard/auth-cookies'
import { userJson } from './auth-connect-mock'

// The unit setup mocks the bootstrap for every page test; this file tests the
// bootstrap itself, so the mock has to go.
vi.unmock('#/libraries/guard/auth-session')

/**
 * The bootstrap races a real UI: the background verify can settle after a
 * logout or a re-login. These tests pin the guard that keeps a stale verify
 * answer from resurrecting a session the user already ended.
 *
 * The boot promise and the store are module-level state, so every test loads
 * fresh module instances and reads the store through the same instance the
 * bootstrap writes to.
 */

const pending: {
  promise: Promise<Profile | null>
  resolve: (profile: Profile | null) => void
  reject: (error: unknown) => void
} = {
  promise: Promise.resolve(null),
  resolve: () => {},
  reject: () => {}
}

interface Profile {
  id: string
  username: string
  email: string
  displayName: string
}

function deferredSession() {
  pending.promise = new Promise<Profile | null>((resolve, reject) => {
    pending.resolve = resolve
    pending.reject = reject
  })
}

const fakeWorker = {
  login: vi.fn<(credentials: never, options?: never) => Promise<Profile>>(),
  refresh: vi.fn<() => Promise<boolean>>(async () => false),
  maybeRefresh: vi.fn<(withinMs: number) => Promise<boolean>>(async () => false),
  restore: vi.fn<() => Promise<void>>(async () => {}),
  session: vi.fn<() => Promise<Profile | null>>(() => pending.promise),
  accessToken: vi.fn<() => Promise<string | null>>(async () => null),
  logout: vi.fn<() => Promise<void>>(async () => {})
}

vi.mock('#/libraries/guard/auth-worker-client', () => ({
  authWorker: () => fakeWorker
}))

async function loadFresh() {
  vi.resetModules()
  const [session, store] = await Promise.all([
    import('#/libraries/guard/auth-session'),
    import('#/libraries/guard/auth-store')
  ])
  return { session, store }
}

describe('auth session bootstrap', () => {
  beforeEach(() => {
    clearTokenCookies()
    deferredSession()
    fakeWorker.restore.mockClear()
    fakeWorker.session.mockClear()
  })

  afterEach(() => {
    clearTokenCookies()
  })

  it('keeps a cached profile across an inconclusive verify — offline is not a sign-out', async () => {
    writeUserCookie(userJson)
    const { session, store } = await loadFresh()

    await session.ensureSessionLoaded()
    expect(store.authStore.state.user).toEqual(userJson)

    // The backend never answered — the cached account stays on screen.
    pending.reject(new Error('network down'))
    await vi.waitFor(() => expect(fakeWorker.session).toHaveBeenCalled())
    expect(store.authStore.state.user).toEqual(userJson)
  })

  it('does not resurrect a session the user logged out of mid-verify', async () => {
    writeUserCookie(userJson)
    const { session, store } = await loadFresh()

    await session.ensureSessionLoaded()
    expect(store.authStore.state.user).toEqual(userJson)

    // The user signs out while the background verify is still in flight.
    store.clearAuth()
    pending.resolve({ ...userJson })
    await vi.waitFor(() => expect(fakeWorker.session).toHaveBeenCalled())

    // The stale answer must not bring the profile back.
    expect(store.authStore.state.user).toBeNull()
  })

  it('applies the confirmed profile when the boot account is still displayed', async () => {
    writeUserCookie(userJson)
    const { session, store } = await loadFresh()

    await session.ensureSessionLoaded()

    const confirmed = { ...userJson, displayName: 'Robert Langdon II' }
    pending.resolve(confirmed)
    await vi.waitFor(() => expect(store.authStore.state.user).toEqual(confirmed))
  })

  it('ends the session when the verify definitely reports it gone', async () => {
    writeUserCookie(userJson)
    const { session, store } = await loadFresh()

    await session.ensureSessionLoaded()
    expect(store.authStore.state.user).toEqual(userJson)

    pending.resolve(null)
    await vi.waitFor(() => expect(store.authStore.state.user).toBeNull())
  })

  it('renders no session when there is no cache and the verify is inconclusive', async () => {
    const { session, store } = await loadFresh()

    const boot = session.ensureSessionLoaded()
    pending.reject(new Error('network down'))
    await boot.catch(() => {})
    await vi.waitFor(() => expect(fakeWorker.session).toHaveBeenCalled())
    expect(store.authStore.state.user).toBeNull()
  })

  it('answers the blocking path with the confirmed profile when no cache exists', async () => {
    const { session, store } = await loadFresh()

    const boot = session.ensureSessionLoaded()
    pending.resolve(userJson)
    await boot

    expect(store.authStore.state.user).toEqual(userJson)
  })
})
