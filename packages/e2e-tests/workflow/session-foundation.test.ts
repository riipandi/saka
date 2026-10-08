import { test, expect } from '@playwright/test'
import type { BrowserContext, Page } from '@playwright/test'

// The session foundation flow: the login route reads the deployment
// configuration, the credential form signs in, a reload restores the
// session from the cookies before the network answers, and sign-out
// returns to the login route. A real Chromium against the debug build —
// the composition-root proof the unit and browser projects cannot give.

const identity = process.env.E2E_IDENTITY || 'admin'
const password = process.env.E2E_PASSWORD || '@dmin123'
const displayName = 'Admin Sistem'

let context: BrowserContext
let page: Page

test.describe.serial('the session foundation flow', () => {
  test('the login route offers only what the configuration enables', async ({ browser }) => {
    context = await browser.newContext()
    page = await context.newPage()

    const configuration = page.waitForRequest('**/api/configuration')
    await page.goto('/login')
    await configuration

    // The OIDC toggle ships disabled — the SSO options stay hidden and the
    // credential form is the only door.
    await expect(page.getByText('Continue with Google')).toHaveCount(0)
    await expect(page.getByText('Continue with GitHub')).toHaveCount(0)
    await expect(page.locator('#identity')).toBeVisible()
    await expect(page.locator('#password')).toBeVisible()
  })

  test('sign in through the form into the signed-in shell', async () => {
    await page.fill('#identity', identity)
    await page.fill('#password', password)
    await page.click('button[type=submit]')

    await expect(page).toHaveURL(/\/overview$/)
    await expect(page.getByText(displayName)).toBeVisible()
  })

  test('a reload restores the session from the cookies', async () => {
    // Hold the session verify so the assertion proves the profile renders
    // from the cookie before the network answers it.
    await page.route('**/rpc/saka.authn.v1.SessionService/GetSession', async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 5000))
      await route.continue()
    })

    await page.reload()
    await expect(page).toHaveURL(/\/overview$/)
    await expect(page.getByText(displayName)).toBeVisible()

    // The delayed verify completes without evicting the session.
    await expect(page).toHaveURL(/\/overview$/, { timeout: 15_000 })
  })

  test('sign out returns to the login route', async () => {
    await page.click('[aria-label="Sign Out"]')
    await expect(page).toHaveURL(/\/login/)
    await expect(page.locator('#identity')).toBeVisible()
  })
})
