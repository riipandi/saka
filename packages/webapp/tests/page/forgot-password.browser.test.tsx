import { Code, ConnectError, createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter
} from '@tanstack/react-router'
import { describe, expect, it } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { ForgotPasswordView } from '#/routes/(auth)/-forgot-password-view'
import { PasswordRecoveryService } from '~/codegen/authn_pb'

/** The stubs the context contract requires; the anonymous views never call
 * the session surfaces. */
const notUsed = async () => {
  throw new Error('session surfaces are not part of this page test')
}

import { AuthContext } from '#/hooks/use-auth'

function wrapper(children: React.ReactNode, transport: ReturnType<typeof createRouterTransport>) {
  return (
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: 0 } } })
      }
    >
      <TransportProvider transport={transport}>
        <AuthContext.Provider
          value={{
            user: null,
            loggedIn: false,
            isLoading: false,
            login: notUsed,
            continueSignIn: notUsed,
            completeSignIn: notUsed,
            verifyPasskeyLogin: notUsed,
            logout: () => {}
          }}
        >
          {children}
        </AuthContext.Provider>
      </TransportProvider>
    </QueryClientProvider>
  )
}

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
  return render(wrapper(<RouterProvider router={router} />, transport))
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
