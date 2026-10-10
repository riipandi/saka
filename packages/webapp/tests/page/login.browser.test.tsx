import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter
} from '@tanstack/react-router'
import { describe, expect, it, vi, type Mock } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { AuthContext } from '#/hooks/use-auth'
import type { AuthLoginContextOptions } from '#/hooks/use-auth'
import type { CompleteSignInFactor, SignInOutcome } from '#/libraries/guard/auth-engine'
import { LoginView } from '#/routes/(auth)/-login-view'
import {
  BeginTotpEnrollmentResponseSchema,
  ConfirmTotpEnrollmentResponseSchema,
  MultifactorService
} from '~/codegen/authn_pb'
import {
  BeginLoginResponseSchema,
  SendReauthenticationCodeResponseSchema,
  WebAuthnService
} from '~/codegen/webauthn_pb'

const USER = {
  id: 'usr_langdon',
  username: 'rlangdon',
  email: 'robert@langdon.dev',
  displayName: 'Robert Langdon'
}

const SIGNED_IN: SignInOutcome = {
  kind: 'signed-in',
  session: {
    user: USER,
    tokens: {
      accessToken: 'access-r',
      accessExpiresAt: Date.now() + 60_000,
      refreshToken: 'refresh-r',
      refreshExpiresAt: Date.now() + 3_600_000,
      sessionId: 'sess_r'
    }
  }
}

const NOT_USED = 'sign in is not part of this test'

/** The never-called stubs the AuthContext contract requires. */
const unused = async () => {
  throw new Error(NOT_USED)
}

async function renderLogin(
  loginOutcome: SignInOutcome | Error,
  completeSignIn: Mock<
    (
      pendingToken: string,
      factor: CompleteSignInFactor,
      options?: AuthLoginContextOptions
    ) => Promise<void>
  >,
  verifyPasskeyLogin: Mock<
    (sessionId: string, credential: string, options?: AuthLoginContextOptions) => Promise<void>
  > = vi.fn(async () => {})
): Promise<{ screen: RenderResult; login: Mock; transportCalls: { beginLogin: number } }> {
  const transportCalls = { beginLogin: 0 }
  const transport = createRouterTransport(({ service }) => {
    service(MultifactorService, {
      beginTotpEnrollment: () =>
        create(BeginTotpEnrollmentResponseSchema, {
          totpId: 'totp_v1',
          name: 'This browser',
          secret: 'JBSWY3DPEHPK3PXP',
          otpauthUri: 'otpauth://totp/Saka:robert@langdon.dev?secret=JBSWY3DPEHPK3PXP&issuer=Saka'
        }),
      confirmTotpEnrollment: () =>
        create(ConfirmTotpEnrollmentResponseSchema, {
          totpId: 'totp_v1',
          recoveryCodes: ['3f9a1c02', '8b2e47d1', '91c4d0aa']
        })
    })
    service(WebAuthnService, {
      sendReauthenticationCode: () => create(SendReauthenticationCodeResponseSchema, {}),
      beginLogin: () => {
        transportCalls.beginLogin += 1
        return create(BeginLoginResponseSchema, {
          options: '{}',
          sessionId: 'wcs_test'
        })
      }
    })
  })

  const login = vi.fn(async () => {
    if (loginOutcome instanceof Error) throw loginOutcome
    return loginOutcome
  })

  const RootRoute = createRootRoute({ component: () => <LoginView /> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/login'] })
  })

  const screen = await render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <TransportProvider transport={transport}>
        <AuthContext.Provider
          value={{
            user: null,
            loggedIn: false,
            isLoading: false,
            login,
            continueSignIn: unused,
            completeSignIn,
            verifyPasskeyLogin,
            logout: () => {}
          }}
        >
          <RouterProvider router={router} />
        </AuthContext.Provider>
      </TransportProvider>
    </QueryClientProvider>
  )
  return { screen, login, transportCalls }
}

async function submitCredentials(screen: RenderResult) {
  await screen.getByLabelText('Username or email').fill('rlangdon')
  await screen.getByLabelText('Password').fill('sophie')
  await screen.getByRole('button', { name: 'Sign in' }).click()
}

describe('Login (browser)', () => {
  it('renders the credentials card', async () => {
    const { screen } = await renderLogin(SIGNED_IN, vi.fn())
    await expect.element(screen.getByLabelText('Username or email')).toBeVisible()
  })

  it('reads a failed sign-in as the server said it', async () => {
    const { screen } = await renderLogin(
      new Error('The credentials do not match an account.'),
      vi.fn()
    )
    await submitCredentials(screen)
    await expect.element(screen.getByText('The credentials do not match an account.')).toBeVisible()
  })

  it('moves to the challenge when the sign-in forked, and spends the bridge there', async () => {
    const challenge: SignInOutcome = {
      kind: 'mfa-challenge',
      pendingToken: 'bridge_1',
      expiresAt: Date.now() + 300_000
    }
    const completeSignIn = vi.fn<
      (
        pendingToken: string,
        factor: CompleteSignInFactor,
        options?: AuthLoginContextOptions
      ) => Promise<void>
    >(async () => {})
    const { screen } = await renderLogin(challenge, completeSignIn)
    await submitCredentials(screen)

    const code = screen.getByLabelText('Verification code')
    await expect.element(code).toBeVisible()
    await code.fill('039471')
    await screen.getByRole('button', { name: 'Verify' }).click()
    await vi.waitFor(() => {
      expect(completeSignIn).toHaveBeenCalledWith(
        'bridge_1',
        { kind: 'code', code: '039471' },
        { redirectTo: undefined }
      )
    })
  })

  it('restarts the sign-in once the bridge expired', async () => {
    const challenge: SignInOutcome = {
      kind: 'mfa-challenge',
      pendingToken: 'bridge_dead',
      expiresAt: Date.now() - 1000
    }
    const completeSignIn = vi.fn<
      (
        pendingToken: string,
        factor: CompleteSignInFactor,
        options?: AuthLoginContextOptions
      ) => Promise<void>
    >(async () => {})
    const { screen } = await renderLogin(challenge, completeSignIn)
    await submitCredentials(screen)

    await expect.element(screen.getByText('The verification window closed')).toBeVisible()
    await screen.getByRole('button', { name: 'Back to sign in' }).click()
    await expect.element(screen.getByLabelText('Username or email')).toBeVisible()
    expect(completeSignIn).not.toHaveBeenCalled()
  })

  it('enrolls through the bridge: scan, confirm, codes shown once, then completes', async () => {
    const challenge: SignInOutcome = {
      kind: 'enrollment-required',
      pendingToken: 'bridge_2',
      expiresAt: Date.now() + 300_000
    }
    const completeSignIn = vi.fn<
      (
        pendingToken: string,
        factor: CompleteSignInFactor,
        options?: AuthLoginContextOptions
      ) => Promise<void>
    >(async () => {})
    const { screen } = await renderLogin(challenge, completeSignIn)
    await submitCredentials(screen)

    await screen.getByLabelText('Name this device').fill('Aegis on tablet')
    await screen.getByRole('button', { name: 'Continue' }).click()

    await expect.element(screen.getByText('JBSWY3DPEHPK3PXP')).toBeVisible()

    await screen.getByLabelText('The code your authenticator renders').fill('039471')
    await screen.getByRole('button', { name: 'Confirm' }).click()

    await expect.element(screen.getByText('Your recovery codes — shown once')).toBeVisible()
    await expect.element(screen.getByText('3f9a1c02')).toBeVisible()
    await screen.getByRole('button', { name: 'I saved my recovery codes' }).click()
    await vi.waitFor(() => {
      // The confirm did not spend the code's time step — the same code the
      // authenticator just rendered completes the sign-in.
      expect(completeSignIn).toHaveBeenCalledWith(
        'bridge_2',
        { kind: 'code', code: '039471' },
        { redirectTo: undefined }
      )
    })
  })

  it('opens the assertion ceremony from the credentials card\u2019s passkey entry', async () => {
    const { screen, transportCalls } = await renderLogin(
      SIGNED_IN,
      vi.fn(async () => {})
    )
    await expect.element(screen.getByLabelText('Username or email')).toBeVisible()
    await screen.getByRole('button', { name: 'Sign in with a passkey' }).click()
    // The ceremony handle answered; the browser's get() then refuses in a
    // test realm with no authenticator — the refusal surfaces in the alert.
    await vi.waitFor(() => {
      expect(transportCalls.beginLogin).toBe(1)
    })
  })

  it('opens the same ceremony from the challenge\u2019s passkey option', async () => {
    const challenge: SignInOutcome = {
      kind: 'mfa-challenge',
      pendingToken: 'bridge_1',
      expiresAt: Date.now() + 300_000
    }
    const completeSignIn = vi.fn<
      (
        pendingToken: string,
        factor: CompleteSignInFactor,
        options?: AuthLoginContextOptions
      ) => Promise<void>
    >(async () => {})
    const { screen, transportCalls } = await renderLogin(challenge, completeSignIn)
    await submitCredentials(screen)

    const passkey = screen.getByRole('button', { name: 'Use a passkey instead' })
    await expect.element(passkey).toBeVisible()
    await passkey.click()
    await vi.waitFor(() => {
      expect(transportCalls.beginLogin).toBe(1)
    })
    // The browser refused before any bridge was spent.
    expect(completeSignIn).not.toHaveBeenCalled()
  })
})
