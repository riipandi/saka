import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import type { TokenBundle } from '#/libraries/guard/auth-engine'

/**
 * A BroadcastChannel stand-in with an inspectable bus: posts fan out to every
 * other instance, never back to the poster — the real channel's contract, and
 * the reason each simulated tab gets its own module instance below.
 */
class FakeChannel {
  static instances: FakeChannel[] = []
  listeners: Array<(event: MessageEvent) => void> = []
  location = { origin: 'http://localhost:3000' }
  constructor(public name: string) {
    FakeChannel.instances.push(this)
  }
  postMessage(data: unknown, targetOrigin?: string | number) {
    void targetOrigin
    for (const bus of FakeChannel.instances) {
      if (bus === this) continue
      bus.receive(data)
    }
  }
  receive(data: unknown) {
    const event = new MessageEvent('message', { data })
    for (const listener of this.listeners) listener(event)
  }
  addEventListener(_type: string, listener: (event: MessageEvent) => void) {
    this.listeners.push(listener)
  }
  removeEventListener(_type: string, listener: (event: MessageEvent) => void) {
    this.listeners = this.listeners.filter((kept) => kept !== listener)
  }
  close() {}
}

const bundle = (refreshToken: string, refreshExpiresAt = Date.now() + 60_000): TokenBundle => ({
  accessToken: `access-${refreshToken}`,
  accessExpiresAt: Date.now() + 900_000,
  refreshToken,
  refreshExpiresAt,
  sessionId: 'sess_v1_zzz'
})

type Sync = typeof import('#/libraries/guard/auth-sync')

/** One simulated tab — a fresh module instance holding its own channel. */
async function openTab() {
  vi.resetModules()
  return await import('#/libraries/guard/auth-sync')
}

describe('auth sync (cross-tab custody bridge)', () => {
  let tabA: Sync
  let tabB: Sync

  beforeEach(async () => {
    FakeChannel.instances = []
    vi.stubGlobal('BroadcastChannel', FakeChannel)
    tabA = await openTab()
    tabB = await openTab()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('stays silent when a tab boots with no session — no null chatter', async () => {
    const received: (TokenBundle | null)[] = []
    tabB.subscribeTokens((tokens) => received.push(tokens))

    // Tab A boots signed-out: restore(null) reports a null custody state.
    tabA.publishTokens(null)

    expect(received).toEqual([])
  })

  it('delivers a custody change to the other tabs, never to the poster', async () => {
    const received: (TokenBundle | null)[] = []
    tabB.subscribeTokens((tokens) => received.push(tokens))

    tabA.publishTokens(bundle('refresh-a'))

    expect(received).toHaveLength(1)
    expect(received[0]?.refreshToken).toBe('refresh-a')
  })

  it('suppresses the echo of an adopted pair — no re-broadcast round trip', async () => {
    const received: (TokenBundle | null)[] = []
    tabB.subscribeTokens((tokens) => {
      received.push(tokens)
      // The adopting tab re-reports the pair through its own listener.
      tabB.publishTokens(tokens)
    })

    tabA.publishTokens(bundle('refresh-a'))

    // One delivery; the adoption re-report died at the echo key.
    expect(received).toHaveLength(1)
  })

  it('delivers a remote sign-out exactly once — the null state is a key too', async () => {
    const received: (TokenBundle | null)[] = []
    tabB.subscribeTokens((tokens) => {
      received.push(tokens)
      // The signing-out tab re-reports the drop through its own listener.
      tabB.publishTokens(tokens)
    })

    tabA.publishTokens(bundle('refresh-a'))
    tabA.publishTokens(null)

    // Compare by the echo key, not by millisecond timestamps.
    expect(received.map((tokens) => tokens?.refreshToken ?? null)).toEqual(['refresh-a', null])
  })

  it('ignores a dead pair instead of adopting and re-reporting its drop', async () => {
    const received: (TokenBundle | null)[] = []
    tabB.subscribeTokens((tokens) => received.push(tokens))

    tabA.publishTokens(bundle('refresh-dead', Date.now() - 1000))

    expect(received).toEqual([])
  })

  it('ignores a malformed message', async () => {
    const received: (TokenBundle | null)[] = []
    tabB.subscribeTokens((tokens) => received.push(tokens))

    const poster = new FakeChannel('saka.auth.session')
    poster.postMessage({ tokens: { accessToken: 1 } }, 0)
    poster.postMessage('garbage', 0)

    expect(received).toEqual([])
  })
})
