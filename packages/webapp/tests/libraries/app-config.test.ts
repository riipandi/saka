import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test'
import { fetcher } from '#/libraries/api-client'
import { readAppConfig } from '#/libraries/app-config'

vi.mock('#/libraries/api-client', async (importOriginal) => ({
  ...(await importOriginal<typeof import('#/libraries/api-client')>()),
  fetcher: vi.fn<typeof fetcher>()
}))

const configData = {
  app: { mode: 'development', base_url: 'http://localhost:3000', timezone: 'UTC' },
  auth: {
    one_time_access_email_as_admin_enabled: false,
    one_time_access_email_as_unauthenticated_enabled: true
  },
  oidc: { enabled: true },
  mailer: { notifications: { announcement_email_enabled: true } }
}

function envelope(data: unknown) {
  return {
    status: 'success',
    message: 'the application configuration',
    data,
    metadata: { status_code: 200, request_id: 'req_x' }
  }
}

describe('readAppConfig', () => {
  beforeEach(() => {
    vi.mocked(fetcher).mockResolvedValue(envelope(configData))
  })

  afterEach(() => {
    vi.mocked(fetcher).mockReset()
  })

  it('parses the public subset off the envelope', async () => {
    const config = await readAppConfig()
    expect(config.app.mode).toBe('development')
    expect(config.oidc.enabled).toBe(true)
    expect(config.auth.one_time_access_email_as_unauthenticated_enabled).toBe(true)
    expect(config.mailer.notifications.announcement_email_enabled).toBe(true)
  })

  it('defaults every toggle to false when the backend omits it', async () => {
    // `omitzero` drops unset and false values — an empty document is the
    // deployment where every public feature is off.
    vi.mocked(fetcher).mockResolvedValue(envelope({}))
    const config = await readAppConfig()
    expect(config.app.mode).toBe('')
    expect(config.oidc.enabled).toBe(false)
    expect(config.auth.one_time_access_email_as_admin_enabled).toBe(false)
  })

  it('strips the admin-scope sections a privileged caller receives', async () => {
    vi.mocked(fetcher).mockResolvedValue(
      envelope({ ...configData, cache: { enable: true }, database: { url: 'postgres://host/db' } })
    )
    const config = await readAppConfig()
    expect(config).not.toHaveProperty('cache')
    expect(config).not.toHaveProperty('database')
  })

  it('refuses a malformed body', async () => {
    vi.mocked(fetcher).mockResolvedValue(envelope({ app: { mode: 42 } }))
    await expect(readAppConfig()).rejects.toThrow(/invalid/i)
  })

  it('refuses an envelope that is not a success', async () => {
    vi.mocked(fetcher).mockResolvedValue({ status: 'error', data: configData })
    await expect(readAppConfig()).rejects.toThrow(/invalid/i)
  })
})
