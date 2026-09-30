import { test, expect } from '@playwright/test'
import type { APIRequestContext, Browser, BrowserContext, Page } from '@playwright/test'

// The passkey ladder: enroll, passwordless sign-in, and the step-up proof —
// driven through the devtool's simulation pages (/debug/passkey/*) with the
// virtual WebAuthn authenticator answering the ceremonies. One serial run on
// ONE context: the authenticator's signature counter must advance in step
// with the server's stored sign_count, so the acts share the context the
// enrollment created — a re-seeded credential would assert with a rewound
// counter and earn the clone refusal, which is the server working as
// intended.

const identity = process.env.E2E_IDENTITY || 'admin'
const password = process.env.E2E_PASSWORD || '@dmin123'
const rpId = 'localhost'

let context: BrowserContext
let page: Page
let accessToken = ''

test.describe.serial('the passkey ladder', () => {
  test('enroll a passkey on the authenticated session', async ({
    browser
  }: {
    browser: Browser
  }) => {
    context = await browser.newContext()
    await context.credentials.install()
    page = await context.newPage()

    accessToken = await signIn(page.request)

    await page.goto('/debug/passkey/enroll')
    await page.fill('#token', accessToken)
    await page.fill('#name', 'E2E key')
    await page.click('[data-testid="enroll"]')

    await expect(page.locator('[data-testid="log"] .ok')).toContainText('enrolled:')
    await expect(page.locator('[data-testid="log"] .ok')).toContainText('E2E key')

    // The passkey the page just registered is the authenticator's own: the
    // later acts answer assertions with it, its counter advancing.
    const held = await context.credentials.get({ rpId })
    expect(held).toHaveLength(1)
  })

  test('sign in passwordless with the enrolled passkey', async () => {
    await page.goto('/debug/passkey/signin')
    await page.click('[data-testid="signin"]')

    await expect(page.locator('[data-testid="log"] .ok').first()).toContainText(
      'signed in as admin'
    )
  })

  test('mint the step-up proof and spend it exactly once', async ({
    request
  }: {
    request: APIRequestContext
  }) => {
    // A live session the guarded call acts on — the passkey sign-in's own
    // session, ended without touching the token that does the acting.
    await page.goto('/debug/passkey/signin')
    await page.click('[data-testid="signin"]')
    await expect(page.locator('[data-testid="log"] .ok').first()).toContainText(
      'signed in as admin'
    )
    const sessionLine = await page
      .locator('[data-testid="log"] .ok')
      .filter({ hasText: 'session: ' })
      .first()
      .textContent()
    const sessionID = sessionLine?.replace('session: ', '').trim() ?? ''
    expect(sessionID).not.toBe('')

    await page.goto('/debug/passkey/stepup')
    await page.fill('#token', accessToken)
    await page.click('[data-testid="stepup"]')
    const proofLine = await page
      .locator('[data-testid="log"] .ok')
      .filter({ hasText: 'proof: ' })
      .first()
      .textContent()
    const proof = proofLine?.replace('proof: ', '').trim() ?? ''
    expect(proof).not.toBe('')

    // The guarded call spends the header proof once.
    const spent = await request.post('/rpc/tango.authn.v1.SessionService/RevokeSession', {
      headers: { Authorization: `Bearer ${accessToken}`, 'X-Tango-Reauthentication': proof },
      data: { id: sessionID }
    })
    expect(spent.status()).toBe(200)

    // The replay answers unauthenticated — the proof died with its spend.
    const replay = await request.post('/rpc/tango.authn.v1.SessionService/RevokeSession', {
      headers: { Authorization: `Bearer ${accessToken}`, 'X-Tango-Reauthentication': proof },
      data: { id: sessionID }
    })
    expect(replay.status()).toBe(401)
  })

  test.afterAll(async () => {
    await context?.close()
  })

  // ---- helpers ----

  async function signIn(request: APIRequestContext): Promise<string> {
    const response = await request.post('/rpc/tango.authn.v1.AuthService/SignIn', {
      data: { identity, password }
    })
    expect(response.status()).toBe(200)
    return (await response.json()).access_token
  }
})
