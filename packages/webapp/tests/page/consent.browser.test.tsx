import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { ConsentView, parseInteractionCallback, type ResumeAnswer } from '#/routes/-consent-view'

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

  it('refuses a foreign url, a protocol-relative one, and garbage', () => {
    expect(parseInteractionCallback('https://evil.example/cb')).toBeNull()
    expect(parseInteractionCallback('//evil.example')).toBeNull()
    expect(parseInteractionCallback('/account/settings')).toBeNull()
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
