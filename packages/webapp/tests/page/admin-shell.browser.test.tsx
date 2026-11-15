import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider
} from '@tanstack/react-router'
import { describe, expect, it, vi } from 'vite-plus/test'
import { type RenderResult } from 'vitest-browser-react'
import { type AuthContextValue } from '#/hooks/use-auth'
import { appStore } from '#/libraries/app.store'
import { clearAuth, setAuthGrants, setAuthUser } from '#/libraries/guard/auth-store'
import { AdminBanner } from '#/routes/(app)/-admin-banner'
import { SideNavbar } from '#/routes/(app)/-sidebar'
import { renderView, signedInAuth } from './view-harness'

const USER = {
  id: 'usr_veteri',
  username: 'veteri',
  email: 'veteri@example.com',
  displayName: 'Veteri'
}

/** Wrap a sidebar or banner mount in the router context its links need. */
function withRouter(children: React.ReactNode, path = '/') {
  const RootRoute = createRootRoute({ component: () => <>{children}</> })
  const router = createRouter({
    routeTree: RootRoute,
    history: createMemoryHistory({ initialEntries: [path] })
  })
  return <RouterProvider router={router} />
}

async function mount(
  children: React.ReactNode,
  opts: { auth?: AuthContextValue; path?: string } = {}
): Promise<RenderResult> {
  return renderView(withRouter(children, opts.path), opts)
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

    const screen = await mount(<AdminBanner />, {
      auth: signedInAuth({ user: USER, stopImpersonation })
    })
    const banner = screen.getByTestId('impersonation-banner')

    await banner.getByRole('button', { name: 'Stop impersonating' }).click()
    expect(stopImpersonation).toHaveBeenCalledOnce()
    clearAuth()
  })

  it('surfaces the refusal when the stop is refused and the way out stays retryable', async () => {
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

    const screen = await mount(<AdminBanner />, {
      auth: signedInAuth({ user: USER, stopImpersonation })
    })
    const banner = screen.getByTestId('impersonation-banner')

    await banner.getByRole('button', { name: 'Stop impersonating' }).click()
    await expect.element(banner.getByText('the delegation already ended elsewhere')).toBeVisible()
    // A refused stop leaves the button alive — a transient failure is one
    // the reader can retry, not one the banner locks them out of.
    await expect.element(banner.getByRole('button', { name: 'Stop impersonating' })).toBeEnabled()
    clearAuth()
  })
})

describe('the sidebar administration section', () => {
  it('shows the administration groups for the administrator snapshot', async () => {
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

    await expect.element(screen.getByText('Identity & Access')).toBeVisible()
    await expect.element(screen.getByRole('link', { name: 'Users' })).toBeVisible()
    await expect.element(screen.getByRole('link', { name: 'Groups' })).toBeVisible()
    await expect.element(screen.getByRole('link', { name: 'Roles' })).toBeVisible()
    clearAuth()
  })

  it('hides every administration group when the snapshot is not an administrator', async () => {
    setAuthUser(USER)
    setAuthGrants({ roles: ['editor'], permissions: ['user:*:read'] })

    const screen = await mount(<SideNavbar />)

    expect(screen.baseElement.textContent).not.toContain('Identity & Access')
    expect(screen.baseElement.textContent).not.toContain('Audit trail')
    clearAuth()
  })

  it('hides items whose slug the snapshot does not carry', async () => {
    setAuthUser(USER)
    // An administrator-shaped role minus the audit slug: the group shows,
    // the audit trail item does not.
    setAuthGrants({
      roles: ['administrator'],
      permissions: ['user:*:list', 'user_group:*:list', 'signup_token:*:list', 'role:*:list']
    })

    const screen = await mount(<SideNavbar />)

    await expect.element(screen.getByText('Identity & Access')).toBeVisible()
    expect(screen.baseElement.textContent).not.toContain('Audit trail')
    clearAuth()
  })
})

const reset = () => {
  window.localStorage.clear()
  appStore.setState((prev) => ({ ...prev, sidebarClosedGroups: [] }))
  clearAuth()
}

describe('the sidebar groups fold', () => {
  const grants = {
    roles: ['administrator'],
    permissions: ['user:*:list', 'user_group:*:list', 'audit_log:*:list']
  }

  it('opens every group by default', async () => {
    reset()
    setAuthUser(USER)
    setAuthGrants(grants)

    const screen = await mount(<SideNavbar />)

    await expect.element(screen.getByRole('link', { name: 'Users' })).toBeVisible()
    await expect.element(screen.getByRole('link', { name: 'Audit trail' })).toBeVisible()
    reset()
  })

  it('hides the links of a folded group and keeps the others', async () => {
    reset()
    setAuthUser(USER)
    setAuthGrants(grants)

    const screen = await mount(<SideNavbar />)
    await screen.getByRole('button', { name: 'Identity & Access' }).click()

    await expect.element(screen.getByRole('link', { name: 'Users' })).not.toBeInTheDocument()
    await expect.element(screen.getByRole('link', { name: 'Audit trail' })).toBeVisible()
    expect(appStore.state.sidebarClosedGroups).toEqual(['Identity & Access'])
    reset()
  })

  it('keeps the group of the current page open over a stored close', async () => {
    reset()
    setAuthUser(USER)
    setAuthGrants(grants)
    appStore.setState((prev) => ({ ...prev, sidebarClosedGroups: ['Identity & Access'] }))

    const screen = await mount(<SideNavbar />, { path: '/admin/users' })

    await expect.element(screen.getByRole('link', { name: 'Users' })).toBeVisible()
    reset()
  })

  it('shows every item and no group button in the icon-only rail', async () => {
    reset()
    setAuthUser(USER)
    setAuthGrants(grants)
    appStore.setState((prev) => ({ ...prev, sidebarClosedGroups: ['Identity & Access'] }))

    const screen = await mount(<SideNavbar collapsed />)

    await expect.element(screen.getByRole('link', { name: 'Users' })).toBeVisible()
    expect(screen.baseElement.textContent).not.toContain('Identity & Access')
    reset()
  })
})
