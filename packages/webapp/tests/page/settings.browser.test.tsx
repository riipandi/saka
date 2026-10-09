import { create } from '@bufbuild/protobuf'
import { createRouterTransport } from '@connectrpc/connect'
import { TransportProvider } from '@connectrpc/connect-query'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render } from 'vitest-browser-react'
import { AuthContext } from '#/hooks/use-auth'
import { authStore, type AuthState } from '#/libraries/guard/auth-store'
import { SettingsView as RouteComponent } from '#/routes/(app)/-settings-view'
import {
  DeleteMyAccountResponseSchema,
  GetCurrentUserResponseSchema,
  UserService
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

const SETTINGS = {
  settings: [
    { key: 'users.self_delete_enabled', value: 'true' },
    { key: 'users.change_email_enabled', value: 'true' },
    { key: 'users.change_username_enabled', value: 'false' }
  ]
}

async function renderSettings() {
  const updateCurrentUser = vi.fn((request: { displayName: string }) => ({
    user: { ...PROFILE, displayName: request.displayName },
    status: 'success',
    message: 'the account was updated'
  }))
  const transport = createRouterTransport(({ service }) => {
    service(UserService, {
      getCurrentUser: () => create(GetCurrentUserResponseSchema, { user: PROFILE }),
      updateCurrentUser,
      deleteMyAccount: () => create(DeleteMyAccountResponseSchema, { status: 'success' })
    })
    service(SettingsService, {
      listPublic: () => create(ListPublicResponseSchema, SETTINGS)
    })
  })

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const logout = vi.fn()
  const auth: AuthState = {
    user: {
      id: PROFILE.id,
      username: PROFILE.username,
      email: PROFILE.email,
      displayName: PROFILE.displayName
    },
    isLoading: false
  }

  const screen = await render(
    <QueryClientProvider client={queryClient}>
      <TransportProvider transport={transport}>
        <AuthContext.Provider
          value={{
            ...auth,
            loggedIn: true,
            login: async () => {},
            continueSignIn: async () => {},
            logout
          }}
        >
          <RouteComponent />
        </AuthContext.Provider>
      </TransportProvider>
    </QueryClientProvider>
  )
  return { screen, updateCurrentUser, logout }
}

describe('Settings (browser)', () => {
  it('renders the account view on real data — no demo stub in sight', async () => {
    const { screen } = await renderSettings()
    await expect.element(screen.getByLabelText('Display name')).toHaveValue('Hermione Granger')
    await expect.element(screen.getByLabelText('Email')).toHaveValue('hermione@hogwarts.edu')
    await expect.element(screen.getByLabelText('Username')).toBeDisabled()
    expect(screen.baseElement.textContent).not.toContain('DummyJSON')
  })

  it('saves the profile and patches the store from the response', async () => {
    const { screen, updateCurrentUser } = await renderSettings()
    await expect.element(screen.getByLabelText('Display name')).toHaveValue('Hermione Granger')

    const displayName = screen.getByLabelText('Display name')
    await displayName.fill('Hermione J. Granger')
    await screen.getByRole('button', { name: 'Save changes' }).click()

    await vi.waitFor(() => {
      expect(updateCurrentUser).toHaveBeenCalled()
      expect(authStore.state.user?.displayName).toBe('Hermione J. Granger')
    })
  })

  it("gates the danger zone by the deployment's public settings", async () => {
    const { screen } = await renderSettings()
    await expect.element(screen.getByRole('button', { name: 'Delete account' })).toBeVisible()
  })
})
