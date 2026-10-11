import { test, expect } from '@playwright/test'
import {
  ADMIN_IDENTITY,
  adminToken,
  bearer,
  closeSignInContexts,
  createVerifiedUser,
  deleteUser,
  isRecord,
  signIn,
  type SeedUser
} from './helpers'

// The admin console on a real deployment: the guard's refusal for a caller
// without the role, the impersonation round trip naming the borrowed
// account, and a ban ending the target's live session. The acts ride the
// wire where the browser is not the point; the screens are driven where
// they are.

let admin: string
let target: SeedUser | undefined

test.describe.serial('the admin console flow', () => {
  test.afterEach(async () => {
    await closeSignInContexts()
  })

  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)
    target = await createVerifiedUser(request, admin, `adm${Date.now() % 100000}`)
  })

  test.afterAll(async ({ request }) => {
    if (target) {
      // The impersonation-ban test may leave the target banned; unban
      // before the delete so the row leaves cleanly.
      await request.post('/rpc/saka.identity.v1.UserService/UnbanUser', {
        headers: bearer(admin),
        data: { id: target.id }
      })
      await deleteUser(request, admin, target.id)
    }
  })

  test('the guard refuses a non-admin with the overview notice', async ({ browser }) => {
    if (!target) throw new Error('the seed never produced the target')
    const page = await signIn(browser, { username: target.username, password: target.password })

    await page.goto('/admin/users')
    // The guard's redirect lands on the overview; the one-shot notice strips
    // its own query after the render, so the notice text — not the query —
    // is the contract the assertion keeps.
    await expect(page).toHaveURL(/\/overview$/, { timeout: 15_000 })
    await expect(page.getByText('Administration is for administrators')).toBeVisible({
      timeout: 15_000
    })
  })

  test('the impersonation round trip names the borrowed account', async ({ browser }) => {
    if (!target) throw new Error('the seed never produced the target')
    const page = await signIn(browser, {
      username: ADMIN_IDENTITY,
      password: process.env.E2E_PASSWORD || '@dmin123'
    })

    // The detail route serves the target directly; the search stays the
    // list's default.
    await page.goto(`/admin/users/${target.id}`)
    await expect(page.getByRole('heading', { name: /E2E/ })).toBeVisible({ timeout: 15_000 })

    await page.getByLabel('Reason').fill('e2e: the impersonation round trip')
    await page.getByRole('button', { name: 'Start impersonating' }).click()

    // The banner is the delegation's evidence: it names the borrowed
    // account and the administrator behind the wheel.
    const banner = page.getByTestId('impersonation-banner')
    await expect(banner).toBeVisible({ timeout: 15_000 })
    await expect(banner).toContainText(`Acting as ${target.username}`)

    // The ride ends where it started: the stop hands the session back —
    // the custody change lands on the overview, the banner gone.
    await banner.getByRole('button', { name: 'Stop impersonating' }).click()
    await expect(page).toHaveURL(/\/overview$/, { timeout: 15_000 })
    await expect(banner).toHaveCount(0)
  })

  test("a ban ends the target's live session", async ({ request }) => {
    if (!target) throw new Error('the seed never produced the target')

    // A live session before the ban: the pair answers.
    const live = await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
      data: { identity: target.username, password: target.password }
    })
    expect(live.status()).toBe(200)
    const parsed: unknown = await live.json()
    const refreshToken =
      isRecord(parsed) && typeof parsed.refresh_token === 'string' ? parsed.refresh_token : ''

    // The ban lands; the session rows a ban withdraws are gone, so the
    // session's own renewal refuses.
    const ban = await request.post('/rpc/saka.identity.v1.UserService/BanUser', {
      headers: bearer(admin),
      data: { id: target.id, reason: 'e2e: the ban ends the live session' }
    })
    expect(ban.status()).toBe(200)

    const renewAfter = await request.post('/rpc/saka.authn.v1.AuthService/Refresh', {
      data: { refresh_token: refreshToken }
    })
    expect(renewAfter.status()).toBeGreaterThanOrEqual(400)
  })
})
