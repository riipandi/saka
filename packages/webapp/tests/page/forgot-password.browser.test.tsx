import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter
} from '@tanstack/react-router'
import { describe, expect, it } from 'vite-plus/test'
import { type RenderResult } from 'vitest-browser-react'
import { ForgotPasswordView } from '#/routes/(auth)/-forgot-password-view'
import { PasswordRecoveryService } from '~/codegen/authn_pb'
import { renderView, signedInAuth } from './view-harness'

async function renderForgot(
  forgotPassword?: () => { status: string; message: string; resetToken: string }
): Promise<RenderResult> {
  const transport = createRouterTransport(({ service }) => {
    service(PasswordRecoveryService, {
      forgotPassword:
        forgotPassword ??
        (() => ({ status: 'success', message: 'the reset code was sent', resetToken: '' }))
    })
  })
  const RootRoute = createRootRoute({ component: () => <ForgotPasswordView /> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/forgot-password'] })
  })
  return renderView(<RouterProvider router={router} />, {
    transport,
    auth: signedInAuth({ loggedIn: false })
  })
}

describe('Forgot password (browser)', () => {
  it('answers the sentence the server gave, whatever the address names', async () => {
    const screen: RenderResult = await renderForgot()
    await screen.getByLabelText('Email').fill('robert@langdon.dev')
    await screen.getByRole('button', { name: 'Send reset code' }).click()
    await expect.element(screen.getByText('the reset code was sent')).toBeVisible()
    await expect.element(screen.getByText('Check your inbox')).toBeVisible()
    // A real deployment carries no token — the dev-aid block stays absent.
    expect(screen.container.textContent).not.toContain('Development mode')
  })

  it('carries the server\u2019s refusal \u2014 the cooldown\u2019s resource_exhausted reads', async () => {
    const screen = await renderForgot(() => {
      throw new ConnectError('too soon', Code.ResourceExhausted)
    })
    await screen.getByLabelText('Email').fill('robert@langdon.dev')
    await screen.getByRole('button', { name: 'Send reset code' }).click()
    await expect.element(screen.getByText('Request failed')).toBeVisible()
    await expect.element(screen.getByText('too soon')).toBeVisible()
  })
})
