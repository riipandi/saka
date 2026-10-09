import { create } from '@bufbuild/protobuf'
import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { VerifyEmailView } from '#/routes/-verify-email-view'
import {
  ConfirmEmailChangeResponseSchema,
  EmailVerificationService,
  RequestEmailChangeResponseSchema,
  SendVerificationEmailResponseSchema,
  type VerifyEmailResponse,
  VerifyEmailResponseSchema
} from '~/codegen/identity_pb'

const CODE = 'AbCdEfGhJkMn'

async function renderVerify(
  initialCode?: string,
  verifyEmail?: (request: { token: string }) => VerifyEmailResponse
) {
  const transport = createRouterTransport(({ service }) => {
    service(EmailVerificationService, {
      sendEmail: () => create(SendVerificationEmailResponseSchema, { status: 'success' }),
      verifyEmail:
        verifyEmail ??
        ((): VerifyEmailResponse => create(VerifyEmailResponseSchema, { status: 'success' })),
      requestEmailChange: () => create(RequestEmailChangeResponseSchema, { status: 'success' }),
      confirmEmailChange: () => create(ConfirmEmailChangeResponseSchema, { status: 'success' })
    })
  })

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const screen = await render(
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>
        <VerifyEmailView initialCode={initialCode} />
      </TransportProvider>
    </QueryClientProvider>
  )
  return { screen }
}

describe('Verify-email page (browser)', () => {
  it('waits for a code when the browser carries none', async () => {
    const { screen } = await renderVerify()
    await expect.element(screen.getByText('Verify your email')).toBeVisible()
    await expect.element(screen.getByRole('button', { name: 'Verify' })).toBeDisabled()
  })

  it('prefills the code from the URL and verifies it', async () => {
    const verifyEmail = vi.fn(() => create(VerifyEmailResponseSchema, { status: 'success' }))
    const { screen } = await renderVerify(CODE, verifyEmail)
    const verify = screen.getByRole('button', { name: 'Verify' })
    await expect.element(verify).toBeEnabled()
    await verify.click()
    await vi.waitFor(() => {
      expect(verifyEmail).toHaveBeenCalled()
      expect(screen.baseElement.textContent).toContain('Email verified')
      expect(screen.baseElement.textContent).toContain('Continue to sign in')
    })
  })

  it('reads the spent or wrong code as a failure, not a crash', async () => {
    const verifyEmail = vi.fn((): VerifyEmailResponse => {
      throw new ConnectError('verification token is invalid or expired', Code.PermissionDenied)
    })
    const { screen } = await renderVerify(CODE, verifyEmail)
    const verify = screen.getByRole('button', { name: 'Verify' })
    await expect.element(verify).toBeEnabled()
    await verify.click()
    await vi.waitFor(() => {
      expect(verifyEmail).toHaveBeenCalled()
      expect(screen.baseElement.textContent).toContain('invalid or expired')
      expect(screen.baseElement.textContent).toContain('Verify your email')
    })
  })
})
