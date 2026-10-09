import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { AuthContext } from '#/hooks/use-auth'
import { EmailView } from '#/routes/(app)/account/-email-view'
import {
  ConfirmEmailChangeResponseSchema,
  EmailVerificationService,
  GetCurrentUserResponseSchema,
  RequestEmailChangeResponseSchema,
  SendVerificationEmailResponseSchema,
  UserService,
  VerifyEmailResponseSchema
} from '~/codegen/identity_pb'
import { ListPublicResponseSchema, SettingsService } from '~/codegen/settings_pb'

const PROFILE = {
  id: 'user_v1ag7h12abcd',
  username: 'hermione',
  email: 'hermione@hogwarts.edu',
  displayName: 'Hermione Granger',
  firstName: 'Hermione',
  lastName: 'Granger',
  emailVerified: true,
  createdAt: '2026-01-01T00:00:00Z',
  metadata: { locale: 'en', timezone: 'UTC' },
  userGroups: [],
  picture: ''
}

function settingsWith(changeEmailEnabled: boolean) {
  return {
    settings: [
      { key: 'users.self_delete_enabled', value: 'true' },
      { key: 'users.change_email_enabled', value: String(changeEmailEnabled) },
      { key: 'users.change_username_enabled', value: 'false' }
    ]
  }
}

async function renderEmail(options: { emailVerified: boolean; changeEmailEnabled: boolean }) {
  const sendEmail = vi.fn(() => create(SendVerificationEmailResponseSchema, { status: 'success' }))
  const requestEmailChange = vi.fn(() =>
    create(RequestEmailChangeResponseSchema, { status: 'success', message: 'sent' })
  )
  const transport = createRouterTransport(({ service }) => {
    service(UserService, {
      getCurrentUser: () =>
        create(GetCurrentUserResponseSchema, {
          user: { ...PROFILE, emailVerified: options.emailVerified }
        })
    })
    service(EmailVerificationService, {
      sendEmail,
      verifyEmail: () => create(VerifyEmailResponseSchema, { status: 'success' }),
      requestEmailChange,
      confirmEmailChange: () => create(ConfirmEmailChangeResponseSchema, { status: 'success' })
    })
    service(SettingsService, {
      listPublic: () => create(ListPublicResponseSchema, settingsWith(options.changeEmailEnabled))
    })
  })

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const screen = await render(
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>
        <AuthContext.Provider
          value={{
            user: {
              id: PROFILE.id,
              username: PROFILE.username,
              email: PROFILE.email,
              displayName: PROFILE.displayName
            },
            loggedIn: true,
            isLoading: false,
            login: async () => {},
            continueSignIn: async () => {},
            logout: () => {}
          }}
        >
          <EmailView />
        </AuthContext.Provider>
      </TransportProvider>
    </QueryClientProvider>
  )
  return { screen, sendEmail, requestEmailChange }
}

describe('Email page (browser)', () => {
  it('renders the verified address without the verification surface', async () => {
    const { screen } = await renderEmail({ emailVerified: true, changeEmailEnabled: true })
    await expect.element(screen.getByText('hermione@hogwarts.edu')).toBeVisible()
    await expect.element(screen.getByText('Verified')).toBeVisible()
    expect(screen.baseElement.textContent).not.toContain('Verify this address')
    expect(screen.baseElement.textContent).not.toContain('Send verification code')
  })

  it('offers the code flow to an unverified address', async () => {
    const { screen, sendEmail } = await renderEmail({
      emailVerified: false,
      changeEmailEnabled: true
    })
    await expect.element(screen.getByText('Not verified')).toBeVisible()
    await expect.element(screen.getByText('Verify this address')).toBeVisible()

    const send = screen.getByRole('button', { name: 'Send verification code' })
    await send.click()
    await vi.waitFor(() => {
      expect(sendEmail).toHaveBeenCalled()
      expect(screen.baseElement.textContent).toContain('valid for one hour')
    })
  })

  it("gates the change flow by the deployment's toggle", async () => {
    const { screen, requestEmailChange } = await renderEmail({
      emailVerified: true,
      changeEmailEnabled: true
    })
    const input = screen.getByLabelText('New address')
    await expect.element(input).toBeEnabled()

    await input.fill('luna@hogwarts.edu')
    await screen.getByRole('button', { name: 'Request change' }).click()
    await vi.waitFor(() => {
      expect(requestEmailChange).toHaveBeenCalled()
      expect(screen.baseElement.textContent).toContain('The confirm code was sent to')
    })
  })

  it('disables the change flow when the deployment turns it off', async () => {
    const { screen, requestEmailChange } = await renderEmail({
      emailVerified: true,
      changeEmailEnabled: false
    })
    await expect.element(screen.getByLabelText('New address')).toBeDisabled()
    expect(screen.baseElement.textContent).toContain('does not allow email changes')

    // A disabled control swallows the click; the gate itself is the proof.
    await screen.getByRole('button', { name: 'Request change' }).click({ force: true })
    expect(requestEmailChange).not.toHaveBeenCalled()
  })
})
