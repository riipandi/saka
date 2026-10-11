import { create } from '@bufbuild/protobuf'
import { TimestampSchema } from '@bufbuild/protobuf/wkt'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { setAuthGrants } from '#/libraries/guard/auth-store'
// The runtime card reads the deployment document over the REST seam; the
// mock answers the fetcher directly.
vi.mock('#/libraries/api-client', () => ({
  fetcher: vi.fn(async (path: string) => {
    if (path === 'configuration') {
      return { status: 'success', data: { app: { mode: 'dev' } } }
    }
    throw new Error(`the test fetcher does not serve ${path}`)
  })
}))
import { AdminSettingsView } from '#/routes/(app)/admin/-settings-view'

setAuthGrants({
  roles: ['administrator'],
  permissions: ['setting:*:read', 'setting:*:update']
})
import {
  type Setting,
  SettingSchema,
  ListResponseSchema,
  SettingsService,
  UpdateResponseSchema,
  ResetResponseSchema
} from '~/codegen/settings_pb'
import { AppConfigService, TestEmailResponseSchema } from '~/codegen/system_pb'

function setting(
  key: string,
  value: string,
  overrides: Partial<Omit<Setting, '$typeName' | '$unknown'>> = {}
): Setting {
  return create(SettingSchema, {
    key,
    value,
    defaultValue: 'false',
    sealed: false,
    public: false,
    ...overrides
  })
}

const CATALOG = [
  setting('auth.signup_email_enabled', 'true', { defaultValue: 'true' }),
  setting('password.min_length', '10', {
    defaultValue: '8',
    updatedAt: create(TimestampSchema, { seconds: 1790000000n, nanos: 0 })
  }),
  setting('access.mode', 'invite', { defaultValue: 'open' }),
  setting('webauthn.user_verification', 'preferred', { defaultValue: 'required' }),
  setting('session.lifetime', '43200', { defaultValue: '43200' }),
  setting('mfa.required', 'false', { defaultValue: 'false' })
]

let updated: Array<{ key: string; value: string }> = []
let resetKey: string | undefined
let testEmailTo: string | undefined

async function renderSettings(extra: Setting[] = []) {
  const transport = createRouterTransport(({ service }) => {
    service(SettingsService, {
      list: () => create(ListResponseSchema, { settings: [...CATALOG, ...extra] }),
      update: (request) => {
        updated.push({ key: request.key, value: request.value })
        const row = [...CATALOG, ...extra].find((item) => item.key === request.key)
        return create(UpdateResponseSchema, {
          setting: create(SettingSchema, {
            key: request.key,
            value: request.value,
            defaultValue: row?.defaultValue ?? 'x',
            sealed: false,
            public: false,
            updatedAt: create(TimestampSchema, { seconds: 1790000000n, nanos: 0 })
          })
        })
      },
      reset: (request) => {
        resetKey = request.key
        const row = [...CATALOG, ...extra].find((item) => item.key === request.key)
        return create(ResetResponseSchema, {
          setting: create(SettingSchema, {
            key: request.key,
            value: row?.defaultValue ?? 'x',
            defaultValue: row?.defaultValue ?? 'x',
            sealed: false,
            public: false
          })
        })
      }
    })
    service(AppConfigService, {
      testEmail: (request) => {
        testEmailTo = request.to
        return create(TestEmailResponseSchema, {
          status: 'success',
          message: 'the message was rendered and submitted'
        })
      }
    })
  })

  const screen: RenderResult = await render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transport}>
        <AdminSettingsView />
      </TransportProvider>
    </QueryClientProvider>
  )
  updated = []
  resetKey = undefined
  testEmailTo = undefined
  return screen
}

describe('Platform settings (browser)', () => {
  it('groups the catalog into the taxonomy tabs and renders each control kind', async () => {
    const screen = await renderSettings()
    await expect.element(screen.getByText('Email at sign-up')).toBeVisible()
    await expect.element(screen.getByText(/Minimum length/)).toBeVisible()
    await expect.element(screen.getByText('overridden').nth(0)).toBeVisible()
    // The boolean renders a switch, the enum a combobox, the number a
    // draft field with its unit badge.
    const flip = screen.getByLabelText('Email at sign-up').element()
    if (!(flip instanceof HTMLElement)) throw new Error('the switch is not an element')
    expect(flip.getAttribute('aria-checked')).toBe('true')
    await expect.element(screen.getByLabelText('Minimum length')).toHaveValue(10)
  })

  it('commits a boolean flip on the switch', async () => {
    const screen = await renderSettings()
    await screen.getByRole('tab', { name: 'Multi-Factor' }).click()
    const flip = screen.getByLabelText('MFA required').element()
    if (!(flip instanceof HTMLElement)) throw new Error('the switch is not an element')
    flip.click()
    await vi.waitFor(() => {
      expect(updated).toEqual([{ key: 'mfa.required', value: 'true' }])
    })
  })

  it('stages a numeric edit and saves it on the button', async () => {
    const screen = await renderSettings()
    const input = screen.getByLabelText('Minimum length')
    await input.fill('12')
    await screen.getByRole('button', { name: 'Save' }).nth(0).click()
    await vi.waitFor(() => {
      expect(updated).toEqual([{ key: 'password.min_length', value: '12' }])
    })
  })

  it('resets an override to the catalog default after the confirmation', async () => {
    const screen = await renderSettings()
    await screen.getByRole('button', { name: 'Reset Minimum length to default' }).click()
    const scope = screen.getByRole('alertdialog')
    const confirm = scope.getByRole('button', { name: 'Reset' }).element()
    if (!(confirm instanceof HTMLElement)) throw new Error('the confirm button is not an element')
    confirm.click()
    await vi.waitFor(() => {
      expect(resetKey).toBe('password.min_length')
    })
  })

  it('picks an enum value and writes it through', async () => {
    const screen = await renderSettings()
    await screen.getByRole('tab', { name: 'Access & Sign-ups' }).click()
    const trigger = screen.getByLabelText('Access mode')
    await expect.element(trigger).toBeVisible()
    const triggerElement = trigger.element()
    if (!(triggerElement instanceof HTMLElement)) throw new Error('the trigger is not an element')
    triggerElement.click()
    await screen.getByRole('option', { name: 'open' }).click()
    await vi.waitFor(() => {
      expect(updated).toEqual([{ key: 'access.mode', value: 'open' }])
    })
  })

  it('sends the mailer probe with the recipient override', async () => {
    const screen = await renderSettings()
    await screen.getByRole('tab', { name: 'Runtime' }).click()
    await screen.getByRole('button', { name: 'Send test email' }).click()
    const dialog = screen.getByRole('dialog')
    await dialog.getByLabelText('Recipient override').fill('probe@example.com')
    const send = dialog.getByRole('button', { name: 'Send', exact: true }).element()
    if (!(send instanceof HTMLElement)) throw new Error('the send button is not an element')
    send.click()
    await expect.element(dialog.getByText('the message was rendered and submitted')).toBeVisible()
    expect(testEmailTo).toBe('probe@example.com')
  })
})
