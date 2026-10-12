import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import {
  ConsentView,
  normalizeUserCode,
  parseInteractionCallback,
  type ResumeAnswer
} from '#/routes/-consent-view'

const CONSENT: ResumeAnswer = {
  kind: 'consent',
  document: {
    interaction_id: 'sess_1',
    client_id: 'cli_1',
    client_name: 'La Forgia',
    requested_scopes: ['openid', 'profile', 'offline_access'],
    consent_required: true,
    login_hint: 'robert@langdon.example'
  }
}

describe('parseInteractionCallback', () => {
  it('accepts the authorize callback shape the provider writes', () => {
    expect(parseInteractionCallback('/oidc/authorize/authn_abc123')).toBe(
      '/oidc/authorize/authn_abc123'
    )
  })

  it('accepts the device shapes — the bare entry and the session resume', () => {
    expect(parseInteractionCallback('/oidc/device')).toBe('/oidc/device')
    expect(parseInteractionCallback('/oidc/device/sess_9')).toBe('/oidc/device/sess_9')
  })

  it('refuses a foreign url, a protocol-relative one, and garbage', () => {
    expect(parseInteractionCallback('https://evil.example/cb')).toBeNull()
    expect(parseInteractionCallback('//evil.example')).toBeNull()
    expect(parseInteractionCallback('/account/settings')).toBeNull()
    expect(parseInteractionCallback('/oidc/device/')).toBeNull()
    expect(parseInteractionCallback(undefined)).toBeNull()
  })
})

async function renderConsent(
  props: Partial<Parameters<typeof ConsentView>[0]> = {}
): Promise<{ screen: RenderResult; onNavigate: ReturnType<typeof vi.fn> }> {
  // The navigation default really leaves the page — every test gets a spy
  // unless it passes its own, so a render never sends the iframe away.
  const onNavigate = vi.fn()
  const screen = await render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: 0 } } })
      }
    >
      <ConsentView
        callback='/oidc/authorize/sess_1'
        onResume={vi.fn().mockResolvedValue(CONSENT)}
        onApprove={vi.fn().mockResolvedValue({ kind: 'redirect', target: 'https://rp.example/cb' })}
        onNavigate={onNavigate}
        {...props}
      />
    </QueryClientProvider>
  )
  return { screen, onNavigate }
}

describe('ConsentView', () => {
  it('renders the client name, the scopes, the hint, and the persistent-access disclosure', async () => {
    const { screen } = await renderConsent()
    await expect.element(screen.getByText('La Forgia')).toBeInTheDocument()
    await expect
      .element(screen.getByRole('heading', { name: 'Persistent access' }))
      .toBeInTheDocument()
    await expect.element(screen.getByText(/robert@langdon.example/)).toBeInTheDocument()
    await expect.element(screen.getByText('profile')).toBeInTheDocument()
  })

  it('hands the browser to the redirect the answer carries', async () => {
    const { screen, onNavigate } = await renderConsent()
    await screen.getByRole('button', { name: 'Allow' }).click()
    await vi.waitFor(() => {
      expect(onNavigate).toHaveBeenCalledWith('https://rp.example/cb')
    })
  })

  it('carries every requested scope in the decision', async () => {
    const onApprove = vi
      .fn()
      .mockResolvedValue({ kind: 'redirect', target: 'https://rp.example/cb' })
    const { screen } = await renderConsent({ onApprove })
    await screen.getByRole('button', { name: 'Allow' }).click()
    await vi.waitFor(() => {
      expect(onApprove).toHaveBeenCalledWith('/oidc/authorize/sess_1', [
        'openid',
        'profile',
        'offline_access'
      ])
    })
  })

  it('offers the leave that posts nothing', async () => {
    const onLeave = vi.fn()
    const { screen } = await renderConsent({ onLeave })
    await screen.getByRole('button', { name: 'Cancel' }).click()
    expect(onLeave).toHaveBeenCalled()
  })

  it('bounces a redirect back to the interaction page over to the login screen', async () => {
    const { screen, onNavigate } = await renderConsent({
      onApprove: vi.fn().mockResolvedValue({ kind: 'redirect', target: '/interaction?callback=x' })
    })
    await screen.getByRole('button', { name: 'Allow' }).click()
    await vi.waitFor(() => {
      expect(onNavigate).toHaveBeenCalledWith(expect.stringContaining('/login?return_to='))
    })
  })
})

async function renderDevice(props: Partial<Parameters<typeof ConsentView>[0]> = {}) {
  const onVerify = vi.fn()
  const onNavigate = vi.fn()
  const screen = await render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: { queries: { retry: false }, mutations: { retry: 0 } }
        })
      }
    >
      <ConsentView callback='/oidc/device' onVerify={onVerify} onNavigate={onNavigate} {...props} />
    </QueryClientProvider>
  )
  return { screen, onVerify, onNavigate }
}

async function typeCode(screen: RenderResult, code: string) {
  const slots = screen.getByRole('textbox').all()
  for (const [index, slot] of slots.entries()) {
    await slot.fill(code[index] ?? '')
  }
}

describe('the device entry', () => {
  it('strips the dashes a reader copies, uppercases, and caps at eight letters', () => {
    expect(normalizeUserCode('abcd-efgh')).toBe('ABCDEFGH')
    expect(normalizeUserCode('bcdf ghjk')).toBe('BCDFGHJK')
    expect(normalizeUserCode('bcdfghjk123')).toBe('BCDFGHJK')
  })

  it('asks for the code and refuses to continue before eight letters stand', async () => {
    const { screen, onVerify } = await renderDevice()
    await expect.element(screen.getByText('Device sign-in')).toBeInTheDocument()
    await typeCode(screen, 'BCDFG')
    await expect.element(screen.getByRole('button', { name: 'Continue' })).toBeDisabled()
    expect(onVerify).not.toHaveBeenCalled()
  })

  it('sends the normalized code and lands on the approved state', async () => {
    const onVerify = vi.fn().mockResolvedValue({ kind: 'approved' })
    const { screen } = await renderDevice({ onVerify })
    await typeCode(screen, 'BCDFGHJK')
    await screen.getByRole('button', { name: 'Continue' }).click()
    await vi.waitFor(() => {
      expect(onVerify).toHaveBeenCalledWith('BCDFGHJK')
    })
    await expect.element(screen.getByText('Device authorized')).toBeInTheDocument()
  })

  it('re-prompts when the code comes back unrecognized', async () => {
    const { screen } = await renderDevice({
      onVerify: vi
        .fn()
        .mockResolvedValue({ kind: 'redirect', target: '/interaction?callback=%2Foidc%2Fdevice' })
    })
    await typeCode(screen, 'BCDFGHJK')
    await screen.getByRole('button', { name: 'Continue' }).click()
    await expect.element(screen.getByText(/not recognized/i)).toBeInTheDocument()
    await expect.element(screen.getByText('Device sign-in')).toBeInTheDocument()
  })

  it('resumes the session in place, asks the question, and approves on the bare 200', async () => {
    const onVerify = vi.fn().mockResolvedValue({
      kind: 'redirect',
      target: '/interaction?callback=%2Foidc%2Fdevice%2Fsess_9'
    })
    const onApprove = vi.fn().mockResolvedValue({ kind: 'approved' })
    const { screen } = await renderDevice({
      onVerify,
      onApprove,
      onResume: vi.fn().mockResolvedValue(CONSENT)
    })
    await typeCode(screen, 'BCDFGHJK')
    await screen.getByRole('button', { name: 'Continue' }).click()
    await expect.element(screen.getByText('La Forgia')).toBeInTheDocument()
    await screen.getByRole('button', { name: 'Allow' }).click()
    await vi.waitFor(() => {
      expect(onApprove).toHaveBeenCalledWith('/oidc/device/sess_9', [
        'openid',
        'profile',
        'offline_access'
      ])
    })
    await expect.element(screen.getByText('Device authorized')).toBeInTheDocument()
  })
})
