import { test, expect } from '@playwright/test'
import {
  adminToken,
  bearer,
  createVerifiedUser,
  deleteUser,
  PNG_BYTES,
  signIn,
  type SeedUser,
  closeSignInContexts
} from './helpers'

// The account's own audit trail on a real deployment: the acts the run
// performs are the records the page reads back. The pagination rides the
// route's search params — page two is a different URL serving different
// rows — and the sort rides it too.

let admin: string
let user: SeedUser | undefined
let token: string

/** The seed the serial describe ran first; a missing one is a run bug. */
function requireSeed(): { user: SeedUser; token: string } {
  if (!user || !token) throw new Error('the beforeAll seed never produced the account')
  return { user, token }
}

test.describe.serial('the account audit flow', () => {
  test.afterEach(async () => {
    await closeSignInContexts()
  })

  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)
    user = await createVerifiedUser(request, admin, `audit${Date.now() % 100000}`)
    token = (
      await (
        await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
          data: { identity: user.username, password: user.password }
        })
      ).json()
    ).access_token
  })

  test.afterAll(async ({ request }) => {
    if (user) await deleteUser(request, admin, user.id)
  })

  test("the acts the run performed render as the account's records", async ({
    request,
    browser
  }) => {
    const { token: bearerToken } = requireSeed()
    // Two more acts, each a record the trail will carry: a profile save and
    // a picture upload, both through the account's own Bearer.
    const save = await request.post('/rpc/saka.identity.v1.UserService/UpdateCurrentUser', {
      headers: bearer(bearerToken),
      data: { firstName: 'E2E', lastName: 'Auditflow', displayName: 'E2E Audit Flow' }
    })
    expect(save.status()).toBe(200)
    const picture = await request.fetch('/api/users/me/profile-picture', {
      method: 'PUT',
      headers: { ...bearer(bearerToken), 'content-type': 'image/png' },
      data: PNG_BYTES
    })
    expect(picture.status()).toBe(200)

    const page = await signIn(browser, requireSeed().user)

    await page.goto('/account/audit')
    await expect(page.getByText('Account updated').first()).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText('Profile picture updated').first()).toBeVisible()
    await expect(page.getByText('Signed in').first()).toBeVisible()
  })

  test('page two is a different URL serving different rows', async ({ request, browser }) => {
    const page = await signIn(browser, requireSeed().user)
    // The act writes records until the trail spans two pages — the page
    // size the wire answers is twenty, so twenty-two updates do it.
    for (let index = 0; index < 22; index += 1) {
      const save = await request.post('/rpc/saka.identity.v1.UserService/UpdateCurrentUser', {
        headers: bearer(requireSeed().token),
        data: {
          firstName: 'E2E',
          lastName: 'Auditflow',
          displayName: `E2E Audit Flow ${index}`
        }
      })
      expect(save.status()).toBe(200)
    }

    await page.goto('/account/audit')
    await expect(page.getByText(/of \d+/)).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: 'Next' }).click()
    await expect(page).toHaveURL(/page=2/)
    const first = await page.locator('body').textContent()
    await page.getByRole('button', { name: 'Previous' }).click()
    await expect(page).toHaveURL(/page=1|\/account\/audit$/)
    const back = await page.locator('body').textContent()
    expect(first).not.toEqual(back)
  })

  test('the sort rides the URL and the rows reorder', async ({ browser }) => {
    const page = await signIn(browser, requireSeed().user)
    await page.goto('/account/audit')
    await page.getByRole('combobox').click()
    await page.getByRole('option', { name: /Event, A/ }).click()
    await expect(page).toHaveURL(/sortBy=event/)
    await expect(page.getByText('Account updated').first()).toBeVisible()
  })
})
