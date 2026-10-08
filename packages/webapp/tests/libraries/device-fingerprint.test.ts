import FingerprintJS from '@fingerprintjs/fingerprintjs'
import { beforeEach, describe, expect, it, vi } from 'vite-plus/test'

vi.mock('@fingerprintjs/fingerprintjs', () => {
  const load = vi.fn<() => Promise<{ get: () => Promise<unknown> }>>(() =>
    Promise.resolve({
      get: () =>
        Promise.resolve({
          visitorId: 'fp-visitor',
          confidence: { score: 1 },
          version: 'v5',
          components: { userAgent: { value: 'Mozilla/5.0 test-agent' } }
        })
    })
  )
  return { default: { load } }
})

/** Fresh module instance per test — the singleton promise is module state. */
async function fresh(): Promise<typeof import('#/libraries/device-fingerprint')> {
  vi.resetModules()
  return import('#/libraries/device-fingerprint')
}

describe('deviceHeaders', () => {
  beforeEach(() => {
    vi.mocked(FingerprintJS.load).mockClear()
  })

  it('answers the fingerprint and the user agent', async () => {
    const { deviceHeaders: headers } = await fresh()

    await expect(headers()).resolves.toEqual({
      'x-device-fingerprint': 'fp-visitor',
      'user-agent': 'Mozilla/5.0 test-agent'
    })
  })

  it('loads the agent once and shares the answer', async () => {
    const { deviceHeaders: headers } = await fresh()

    const first = headers()
    const second = headers()
    await Promise.all([first, second])

    expect(FingerprintJS.load).toHaveBeenCalledTimes(1)
    await expect(second).resolves.toEqual(await first)
  })

  it('answers no headers when the browser refuses the fingerprinting', async () => {
    vi.mocked(FingerprintJS.load).mockRejectedValueOnce(new Error('blocked'))
    const { deviceHeaders: headers } = await fresh()

    await expect(headers()).resolves.toEqual({})
  })
})
