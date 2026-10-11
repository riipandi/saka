import { create } from '@bufbuild/protobuf'
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
import { AuthContext } from '#/hooks/use-auth'
import { SignupView } from '#/routes/(auth)/-signup-view'
import { SignupResponseSchema, SignupService, UserSchema } from '~/codegen/identity_pb'
import {
  ListPublicResponseSchema,
  PublicSettingSchema,
  SettingsService
} from '~/codegen/settings_pb'

/** The stubs the context contract requires; the anonymous views never call
 * the session surfaces. */
const notUsed = async () => {
  throw new Error('session surfaces are not part of this page test')
}

interface SignupCapture {
  firstName: string
  lastName: string
  username?: string
  email: string
  password: string
  token?: string
}

function settingsTransport(
  entries: Array<[string, string]>,
  signup?: (request: SignupCapture) => { status: string; message: string }
): ReturnType<typeof createRouterTransport> {
  return createRouterTransport(({ service }) => {
    service(SettingsService, {
      listPublic: () => {
        const settings = entries.map(([key, value]) => create(PublicSettingSchema, { key, value }))
        return create(ListPublicResponseSchema, { settings })
      }
    })
    service(SignupService, {
      signup: (request) => {
        signup?.({
          firstName: request.firstName,
          lastName: request.lastName,
          username: request.username,
          email: request.email,
          password: request.password,
          token: request.token
        })
        return create(SignupResponseSchema, {
          user: create(UserSchema, { id: 'usr_new', email: request.email }),
          status: 'success',
          message: 'the account was created'
        })
      }
    })
  })
}

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
            requestOneTimeAccess: notUsed,
            exchangeOneTimeToken: notUsed,
            stopImpersonation: async () => {
              throw new Error('the impersonation banner is not part of this page test')
            },
            impersonateUser: async () => {
              throw new Error('impersonation is not part of this page test')
            },
            logout: () => {}
          }}
        >
          {children}
        </AuthContext.Provider>
      </TransportProvider>
    </QueryClientProvider>
  )
}

async function renderSignup(
  entries: Array<[string, string]>,
  signup?: (request: SignupCapture) => { status: string; message: string },
  prefill?: string
): Promise<RenderResult> {
  const RootRoute = createRootRoute({ component: () => <SignupView prefill={prefill} /> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/signup'] })
  })
  return render(wrapper(<RouterProvider router={router} />, settingsTransport(entries, signup)))
}

const OPEN: Array<[string, string]> = [
  ['access.mode', 'open'],
  ['auth.signup_username_enabled', 'false'],
  ['auth.require_username', 'false'],
  ['auth.verify_email_at_signup', 'false']
]

async function fillAndSubmit(view: RenderResult, overrides: Partial<Record<string, string>> = {}) {
  await view.getByLabelText('First name').fill(overrides.firstName ?? 'Robert')
  await view.getByLabelText('Last name').fill(overrides.lastName ?? 'Langdon')
  if (overrides.username !== undefined) {
    await view.getByLabelText('Username', { exact: false }).fill(overrides.username)
  }
  await view.getByLabelText('Email').fill(overrides.email ?? 'robert@langdon.dev')
  await view.getByLabelText('Password').fill(overrides.password ?? 's1very-long-password')
  const tokenBoxes = view.getByLabelText(/Invitation token/).elements()
  if (tokenBoxes.length > 0) {
    await view.getByLabelText(/Invitation token/).fill(overrides.token ?? 'stv_invite_raw')
  }
  await view.getByRole('button', { name: 'Create account' }).click()
}

describe('the signup screen', () => {
  it('renders the open-mode shape and sends the account fields without a token', async () => {
    const sent: SignupCapture[] = []
    const view = await renderSignup(OPEN, (request) => {
      sent.push(request)
      return { status: 'success', message: 'the account was created' }
    })

    // The open mode advertises no invitation field and, with the username
    // identity off, no username either.
    await expect.element(view.getByLabelText('First name')).toBeVisible()
    expect(view.baseElement.textContent).not.toContain('Invitation token')
    expect(view.baseElement.textContent).not.toContain('Username')

    await fillAndSubmit(view)

    await expect.poll(() => sent.length).toBe(1)
    expect(sent[0]).toMatchObject({
      firstName: 'Robert',
      lastName: 'Langdon',
      email: 'robert@langdon.dev'
    })
    expect(sent[0]?.username).toBe('')
    expect(sent[0]?.token).toBe('')
  })

  it("refuses a duplicate account with the server's sentence verbatim", async () => {
    const view = await renderSignup(OPEN, () => {
      throw new ConnectError('account already exists', Code.AlreadyExists)
    })

    await fillAndSubmit(view)
    await expect.element(view.getByText('account already exists')).toBeVisible()
  })

  it('requires a username only when the toggles demand one', async () => {
    const view = await renderSignup(
      [...OPEN, ['auth.signup_username_enabled', 'true'], ['auth.require_username', 'true']],
      () => {
        throw new ConnectError('account already exists', Code.AlreadyExists)
      }
    )

    await fillAndSubmit(view, { username: '' })
    await expect.element(view.getByText('Username is required.')).toBeVisible()

    // The pattern is the column's: 3-32 letters, digits, underscores.
    await view.getByLabelText('Username', { exact: false }).fill('robert langdon!')
    await view.getByRole('button', { name: 'Create account' }).click()
    await expect.element(view.getByText(/letters, digits, and underscores/)).toBeVisible()

    await view.getByLabelText('Username', { exact: false }).fill('rlangdon')
    await view.getByRole('button', { name: 'Create account' }).click()
    await expect.element(view.getByText('account already exists')).toBeVisible()
  })

  it('prefills the invitation token and sends it under the invite mode', async () => {
    const sent: SignupCapture[] = []
    const view = await renderSignup(
      [['access.mode', 'invite']],
      (request) => {
        sent.push(request)
        return { status: 'success', message: 'the account was created' }
      },
      'stv_from_the_invitation'
    )

    const tokenField = view.getByLabelText('Invitation token')
    await expect.element(tokenField).toBeVisible()
    await expect.element(tokenField).toHaveValue('stv_from_the_invitation')

    await fillAndSubmit(view, { token: 'stv_from_the_invitation' })
    await expect.poll(() => sent.length).toBe(1)
    expect(sent[0]?.token).toBe('stv_from_the_invitation')
  })

  it("surfaces an invalid invitation with the server's sentence verbatim", async () => {
    const view = await renderSignup([['access.mode', 'invite']], () => {
      throw new ConnectError('signup token is invalid or expired', Code.PermissionDenied)
    })

    await fillAndSubmit(view)
    await expect.element(view.getByText('signup token is invalid or expired')).toBeVisible()
  })

  it('answers the absent access.mode with the invite shape', async () => {
    // A settings row that cannot answer keeps the catalog default — the
    // invite mode — so an unreadable deployment still asks for the token.
    const view = await renderSignup([])

    await expect.element(view.getByLabelText('Invitation token')).toBeVisible()
  })
})
