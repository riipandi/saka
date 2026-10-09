import { test, expect } from '@playwright/test'
import type { APIRequestContext, BrowserContext, Page } from '@playwright/test'

// The session foundation flow: the login route reads the deployment
// configuration, the sign-in options follow the connection store, the
// credential form signs in, a reload restores the session from the cookies
// before the network answers, and sign-out returns to the login route. A
// real Chromium against the webServer's build — the composition-root proof
// the unit and browser projects cannot give.

const identity = process.env.E2E_IDENTITY || 'admin'
const password = process.env.E2E_PASSWORD || '@dmin123'
const displayName = 'Admin Sistem'

let context: BrowserContext
let page: Page

/** Sign in over the wire and answer the Bearer the CRUD procedures judge. */
async function adminToken(request: APIRequestContext): Promise<string> {
  const response = await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
    data: { identity, password }
  })
  expect(response.status()).toBe(200)
  return (await response.json()).access_token
}

/** Lift the created connection's id off the answer without an unsafe assertion. */
function readConnectionId(body: unknown): string {
  if (typeof body !== 'object' || body === null || !('connection' in body)) {
    throw new Error('the create answered no connection')
  }
  const connection = body.connection
  if (
    typeof connection !== 'object' ||
    connection === null ||
    !('id' in connection) ||
    typeof connection.id !== 'string'
  ) {
    throw new Error('the create answered no connection id')
  }
  return connection.id
}

test.describe.serial('the session foundation flow', () => {
  test('the login route offers only what the store enables', async ({ browser }) => {
    context = await browser.newContext()
    page = await context.newPage()

    const configuration = page.waitForRequest('**/api/configuration')
    await page.goto('/login')
    await configuration

    // No connection row ships seeded: the SSO block stays hidden and the
    // credential form is the only door.
    await expect(page.locator('#login-card a[href*="/oauth/"]')).toHaveCount(0)
    await expect(page.locator('#identity')).toBeVisible()
    await expect(page.locator('#password')).toBeVisible()
  })

  test('an enabled connection earns its button and its door', async () => {
    const token = await adminToken(page.request)
    const bearer = { authorization: `Bearer ${token}` }

    const create = await page.request.post('/rpc/saka.authn.v1.OAuthSSOService/CreateConnection', {
      headers: bearer,
      data: {
        kind: 'builtin',
        provider: 'google',
        displayName: 'Google',
        clientId: 'e2e-client-id',
        clientSecret: 'e2e-client-secret',
        enabled: true
      }
    })
    expect(create.status()).toBe(200)
    const connectionId = readConnectionId(await create.json())

    try {
      // A fresh load reads the store: one button, labeled by the row.
      await page.goto('/login')
      const button = page.locator('#login-card a[href="/oauth/google/start"]')
      await expect(button).toHaveText(/Google/)

      // The start route is the flow's door: a 302 out to the provider.
      const start = await page.request.get('/oauth/google/start', { maxRedirects: 0 })
      expect(start.status()).toBe(302)
      expect(start.headers().location).toMatch(/accounts\.google\.com/)
    } finally {
      // The row the act created leaves with the act.
      const del = await page.request.post('/rpc/saka.authn.v1.OAuthSSOService/DeleteConnection', {
        headers: bearer,
        data: { id: connectionId }
      })
      expect(del.status()).toBe(200)
    }

    await page.goto('/login')
    await expect(page.locator('#login-card a[href*="/oauth/"]')).toHaveCount(0)
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
