import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider
} from '@tanstack/react-router'
import { describe, expect, it, vi } from 'vite-plus/test'
import { render, type RenderResult } from 'vitest-browser-react'
import { AuthContext } from '#/hooks/use-auth'
import { clearAuth, setAuthGrants, setAuthUser } from '#/libraries/guard/auth-store'
import { AdminBanner } from '#/routes/(app)/-admin-banner'
import { SideNavbar } from '#/routes/(app)/-sidebar'

const USER = {
  id: 'usr_veteri',
  username: 'veteri',
  email: 'veteri@example.com',
  displayName: 'Veteri'
}

/** Wrap a sidebar or banner mount in the router context its links need. */
function withRouter(children: React.ReactNode) {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] })
  })
  return <RouterProvider router={router} />
}

function authContext(stopImpersonation: () => Promise<void>) {
  return {
    user: USER,
    loggedIn: true,
    isLoading: false,
    login: vi.fn(),
    continueSignIn: vi.fn(),
    completeSignIn: vi.fn(),
    verifyPasskeyLogin: vi.fn(),
    requestOneTimeAccess: vi.fn(),
    exchangeOneTimeToken: vi.fn(),
    stopImpersonation,
    impersonateUser: vi.fn(async () => {}),
    logout: vi.fn()
  }
}

async function mount(children: React.ReactNode): Promise<RenderResult> {
  return render(withRouter(children))
}

describe('the impersonation banner', () => {
  it('hides for an ordinary session — no actor pair, no banner', async () => {
    setAuthUser(USER)
    setAuthGrants({ roles: ['editor'], permissions: ['user:*:read'] })

    const screen = await mount(<AdminBanner />)

    expect(screen.baseElement.textContent).not.toContain('Acting as')
    clearAuth()
  })

  it('names the borrowed account and the administrator behind it', async () => {
    setAuthUser(USER)
    setAuthGrants({
      roles: ['editor'],
      permissions: [],
      actorId: 'usr_langdon',
      actorUsername: 'rlangdon'
    })

    const screen = await mount(<AdminBanner />)
    const banner = screen.getByTestId('impersonation-banner')

    await expect.element(banner).toBeVisible()
    expect(banner.element().textContent).toContain('Acting as veteri')
    expect(banner.element().textContent).toContain('impersonated by rlangdon')
    clearAuth()
  })

  it('falls back to the actor id when the username claim did not survive the renewal', async () => {
    setAuthUser(USER)
    setAuthGrants({ roles: ['editor'], permissions: [], actorId: 'usr_langdon' })

    const screen = await mount(<AdminBanner />)
    const banner = screen.getByTestId('impersonation-banner')

    await expect.element(banner).toBeVisible()
    expect(banner.element().textContent).toContain('impersonated by usr_langdon')
    clearAuth()
  })

  it('stops the delegation: the context call resolves and the banner is done', async () => {
    setAuthUser(USER)
    setAuthGrants({
      roles: ['editor'],
      permissions: [],
      actorId: 'usr_langdon',
      actorUsername: 'rlangdon'
    })
    const stopImpersonation = vi.fn(async () => {})

    const screen = await mount(
      <AuthContext.Provider value={authContext(stopImpersonation)}>
        <AdminBanner />
      </AuthContext.Provider>
    )
    const banner = screen.getByTestId('impersonation-banner')

    await banner.getByRole('button', { name: 'Stop impersonating' }).click()
    expect(stopImpersonation).toHaveBeenCalledOnce()
    clearAuth()
  })

  it('surfaces the refusal when the stop is refused and keeps the way out disabled', async () => {
    setAuthUser(USER)
    setAuthGrants({
      roles: ['editor'],
      permissions: [],
      actorId: 'usr_langdon',
      actorUsername: 'rlangdon'
    })
    const stopImpersonation = vi.fn(async () => {
      throw new Error('the delegation already ended elsewhere')
    })

    const screen = await mount(
      <AuthContext.Provider value={authContext(stopImpersonation)}>
        <AdminBanner />
      </AuthContext.Provider>
    )
    const banner = screen.getByTestId('impersonation-banner')

    await banner.getByRole('button', { name: 'Stop impersonating' }).click()
    await expect.element(banner.getByText('the delegation already ended elsewhere')).toBeVisible()
    await expect.element(banner.getByRole('button', { name: 'Stop impersonating' })).toBeDisabled()
    clearAuth()
  })
})

describe('the sidebar administration section', () => {
  it('shows the Administration section for the administrator snapshot', async () => {
    setAuthUser(USER)
    // The mint carries the role AND the catalog slugs the role grants —
    // the sidebar judges the slugs, the same facts the pages consume.
    setAuthGrants({
      roles: ['administrator'],
      permissions: [
        'user:*:list',
        'user_group:*:list',
        'signup_token:*:list',
        'audit_log:*:list',
        'role:*:list'
      ]
    })

    const screen = await mount(<SideNavbar />)

    await expect.element(screen.getByText('Administration')).toBeVisible()
    await expect.element(screen.getByRole('link', { name: 'Users' })).toBeVisible()
    await expect.element(screen.getByRole('link', { name: 'Groups' })).toBeVisible()
    await expect.element(screen.getByRole('link', { name: 'Roles' })).toBeVisible()
    clearAuth()
  })

  it('hides the Administration section when the snapshot is not an administrator', async () => {
    setAuthUser(USER)
    setAuthGrants({ roles: ['editor'], permissions: ['user:*:read'] })

    const screen = await mount(<SideNavbar />)

    expect(screen.baseElement.textContent).not.toContain('Administration')
    clearAuth()
  })

  it('hides items whose slug the snapshot does not carry', async () => {
    setAuthUser(USER)
    // An administrator-shaped role minus the audit slug: the section shows,
    // the audit trail item does not.
    setAuthGrants({
      roles: ['administrator'],
      permissions: ['user:*:list', 'user_group:*:list', 'signup_token:*:list', 'role:*:list']
    })

    const screen = await mount(<SideNavbar />)

    await expect.element(screen.getByText('Administration')).toBeVisible()
    expect(screen.baseElement.textContent).not.toContain('Audit trail')
    clearAuth()
  })
})
