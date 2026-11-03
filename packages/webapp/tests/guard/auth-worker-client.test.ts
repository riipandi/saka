import { describe, expect, it, vi } from 'vite-plus/test'
import { createEngineCell } from '#/libraries/guard/auth-worker-client'
import type { AuthWorkerClient } from '#/libraries/guard/auth-worker-client'

/**
 * The cell is the dead-worker recovery's seam: a caller holds the cell,
 * never an engine. These tests prove the three behaviors the recovery
 * leans on — the revive re-runs what the dead engine never settled, a
 * call no engine ever answers rejects at the bound, and a settled call is
 * left alone by the swap.
 */

/** An unexercised method answers with a rejection, so a stray call fails loudly. */
const unreached = () => Promise.reject(new Error('the stub does not implement this method'))

function clientStub(overrides: Partial<AuthWorkerClient>): AuthWorkerClient {
  return {
    login: unreached,
    continueSignIn: unreached,
    completeSignIn: unreached,
    verifyPasskeyLogin: unreached,
    requestOneTimeAccess: unreached,
    exchangeOneTimeToken: unreached,
    stopImpersonation: unreached,
    impersonate: unreached,
    refresh: unreached,
    maybeRefresh: unreached,
    restore: unreached,
    session: unreached,
    accessToken: unreached,
    authorization: unreached,
    logout: unreached,
    ...overrides
  }
}

describe('the engine cell', () => {
  it('re-runs a call the first engine never settled against the revive', async () => {
    const dead = clientStub({ session: () => new Promise(() => {}) })
    const cell = createEngineCell(dead, 5_000)
    const pending = cell.run((current) => current.session())

    const session = vi.fn<() => Promise<null>>(() => Promise.resolve(null))
    cell.revive(clientStub({ session }))

    await expect(pending).resolves.toBeNull()
    expect(session).toHaveBeenCalledTimes(1)
  })

  it('rejects a call neither engine nor error event answers, at the bound', async () => {
    const dead = clientStub({ refresh: () => new Promise(() => {}) })
    const cell = createEngineCell(dead, 20)

    await expect(cell.run((current) => current.refresh())).rejects.toThrow(/did not answer/)
  })

  it('leaves a settled call alone when the engine is swapped', async () => {
    const firstSession = vi.fn<() => Promise<null>>(() => Promise.resolve(null))
    const cell = createEngineCell(clientStub({ session: firstSession }), 5_000)
    const promise = cell.run((current) => current.session())
    // A settle is a microtask; in production the revive rides the worker's
    // error event, always after the queue drains. Match that ordering here.
    await Promise.resolve()

    const replacementSession = vi.fn<() => Promise<null>>(() => Promise.resolve(null))
    cell.revive(clientStub({ session: replacementSession }))

    await expect(promise).resolves.toBeNull()
    expect(firstSession).toHaveBeenCalledTimes(1)
    expect(replacementSession).not.toHaveBeenCalled()
  })

  it('keeps serving the replacement on the calls after the revive', async () => {
    const deadLogout = vi.fn<() => Promise<void>>(() => Promise.resolve())
    const cell = createEngineCell(clientStub({ logout: deadLogout }), 5_000)
    cell.revive(clientStub({ logout: vi.fn<() => Promise<void>>(() => Promise.resolve()) }))

    await cell.run((current) => current.logout())

    expect(deadLogout).not.toHaveBeenCalled()
  })
})
