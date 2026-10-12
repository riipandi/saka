import { test, expect, type APIRequestContext, type Page } from '@playwright/test'
import { createHash, randomBytes } from 'node:crypto'
import { adminToken, bearer, closeSignInContexts, isRecord, signIn } from './helpers'

// The interaction flows on a real deployment: the consent page the
// provider's redirect opens, the login round-trip an anonymous visitor
// takes, the silent completion a prior consent buys, and the device
// grant's verification. The screens are the browser tests' subject; this
// file holds the composition — the provider's 302, the SPA's dialect, the
// guard, and the real rows.
//
// The consent rows persist, so every first-approval act owns a client of
// its own: the anonymous walk and the signed-in walk answer different
// clients, and the silent walk reuses the signed-in one.

const RECEIVER_ORIGIN = 'https://rp-e2e.example'
const RECEIVER = `${RECEIVER_ORIGIN}/callback`

interface TestClient {
  id: string
  redirectUri: string
}

let admin: string
let anonymousClient: TestClient
let signedInClient: TestClient
let deviceClient: TestClient

function pkce(): { verifier: string; challenge: string } {
  const verifier = randomBytes(32).toString('base64url')
  const challenge = createHash('sha256').update(verifier).digest('base64url')
  return { verifier, challenge }
}

function authorizeUrl(client: TestClient, challenge: string, state: string): string {
  const params = new URLSearchParams({
    response_type: 'code',
    client_id: client.id,
    redirect_uri: client.redirectUri,
    scope: 'openid profile',
    state,
    code_challenge: challenge,
    code_challenge_method: 'S256'
  })
  return `/oidc/authorize?${params.toString()}`
}

/** The receiver is not a real host — the interception answers it. */
async function interceptReceiver(page: Page): Promise<void> {
  await page.context().route(`${RECEIVER_ORIGIN}/**`, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'text/html',
      body: '<html><body>receiver</body></html>'
    })
  )
}

async function createClient(
  request: APIRequestContext,
  name: string,
  skipConsent: boolean
): Promise<TestClient> {
  const created = await request.post('/rpc/saka.federation.v1.OidcClientService/CreateClient', {
    headers: bearer(admin),
    data: {
      id: `e2e-interaction-${name}-${Date.now()}`,
      name: `E2E Interaction ${name}`,
      callbackUrls: [RECEIVER],
      isPublic: true,
      skipConsent
    }
  })
  expect(created.ok()).toBeTruthy()
  const body = (await created.json()) as unknown
  expect(isRecord(body)).toBeTruthy()
  if (!isRecord(body) || !isRecord(body.client))
    throw new Error('the create answer carries no client')
  const clientId = body.client.id
  expect(typeof clientId).toBe('string')
  if (typeof clientId !== 'string') throw new Error('the client id is not a string')
  return { id: clientId, redirectUri: RECEIVER }
}

async function deleteClient(request: APIRequestContext, id: string): Promise<void> {
  await request.post('/rpc/saka.federation.v1.OidcClientService/DeleteClient', {
    headers: bearer(admin),
    data: { id }
  })
}

test.describe.serial('the interaction flows', () => {
  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)
    anonymousClient = await createClient(request, 'anonymous', false)
    signedInClient = await createClient(request, 'consent', false)
    deviceClient = await createClient(request, 'device', false)
  })

  test.afterAll(async ({ request }) => {
    for (const client of [anonymousClient, signedInClient, deviceClient]) {
      if (client) await deleteClient(request, client.id)
    }
  })

  test.afterEach(async () => {
    await closeSignInContexts()
  })

  test('an anonymous visitor signs in and comes back to the question', async ({ browser }) => {
    const context = await browser.newContext()
    const page = await context.newPage()
    await interceptReceiver(page)
    const { challenge } = pkce()
    await page.goto(authorizeUrl(anonymousClient, challenge, 'e2e-anon'))
    // The interaction page bounced the stranger to the login screen, the
    // return trip armed.
    await expect(page).toHaveURL(/\/login\?return_to=/, { timeout: 15_000 })
    await page.fill('#identity', 'admin')
    await page.fill('#password', '@dmin123')
    await page.click('button[type=submit]')
    // The return lands back on the question, the callback intact.
    await expect(page).toHaveURL(/\/interaction\?callback=/, { timeout: 15_000 })
    await expect(page.getByText('E2E Interaction anonymous')).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: 'Allow' }).click()
    await expect(page).toHaveURL(new RegExp(`^${RECEIVER}\\?code=`), { timeout: 15_000 })
    expect(new URL(page.url()).searchParams.get('state')).toBe('e2e-anon')
    expect(new URL(page.url()).searchParams.get('iss')).toBeTruthy()
  })

  test('a signed-in account answers the consent question and lands on the receiver', async ({
    browser
  }) => {
    const page = await signIn(browser, { username: 'admin', password: '@dmin123' })
    await interceptReceiver(page)
    const { verifier, challenge } = pkce()
    await page.goto(authorizeUrl(signedInClient, challenge, 'e2e-consent'))
    // The provider redirected into the interaction page; the question stands.
    await expect(page).toHaveURL(/\/interaction\?callback=/, { timeout: 15_000 })
    await expect(page.getByText('E2E Interaction consent')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('button', { name: 'Allow' })).toBeVisible()
    await page.getByRole('button', { name: 'Allow' }).click()
    // The decision's redirect hands the browser to the relying party.
    await expect(page).toHaveURL(new RegExp(`^${RECEIVER}\\?code=`), { timeout: 15_000 })
    expect(new URL(page.url()).searchParams.get('state')).toBe('e2e-consent')
    expect(new URL(page.url()).searchParams.get('iss')).toBeTruthy()
    // The verifier is never spent here — the code exchange is the RP's own act.
    expect(verifier).toBeTruthy()
  })

  test('a consent the account already gave completes silently', async ({ browser }) => {
    const page = await signIn(browser, { username: 'admin', password: '@dmin123' })
    await interceptReceiver(page)
    const { challenge } = pkce()
    // The prior approval covers this client: no question stands.
    await page.goto(authorizeUrl(signedInClient, challenge, 'e2e-silent'))
    await expect(page).toHaveURL(new RegExp(`^${RECEIVER}\\?code=`), { timeout: 15_000 })
    await expect(page).not.toHaveURL(/\/interaction/)
  })

  test('the device grant approves from the code entry and the token arrives', async ({
    browser,
    request
  }) => {
    const page = await signIn(browser, { username: 'admin', password: '@dmin123' })
    // The device asks for its codes.
    const issued = await request.post('/oidc/device_authorization', {
      form: { client_id: deviceClient.id, scope: 'openid profile' }
    })
    expect(issued.ok()).toBeTruthy()
    const codes = asCodes(await issued.json())
    if (!codes) throw new Error('the device authorization answer is not the codes document')
    // The browser visits the verification URI the device displays.
    await page.goto(codes.verification_uri)
    await expect(page).toHaveURL(/\/interaction\?callback=/, { timeout: 15_000 })
    await expect(page.getByText('Device sign-in')).toBeVisible({ timeout: 15_000 })
    // The code the device shows, letter by letter into the slots.
    const slots = page.getByRole('textbox')
    await expect(slots).toHaveCount(8)
    for (const [index, letter] of codes.user_code.split('').entries()) {
      await slots.nth(index).fill(letter)
    }
    await page.getByRole('button', { name: 'Continue' }).click()
    // The question stands on the same screen; the approval answers it.
    await expect(page.getByText('E2E Interaction device')).toBeVisible({ timeout: 15_000 })
    await page.getByRole('button', { name: 'Allow' }).click()
    await expect(page.getByText('Device authorized')).toBeVisible({ timeout: 15_000 })
    // The device polls and its token arrives.
    const polled = await request.post('/oidc/token', {
      form: {
        grant_type: 'urn:ietf:params:oauth:grant-type:device_code',
        device_code: codes.device_code,
        client_id: deviceClient.id
      }
    })
    expect(polled.ok()).toBeTruthy()
    const tokens = asTokens(await polled.json())
    if (!tokens) throw new Error('the token answer is not a token document')
    expect(tokens.access_token).toBeTruthy()
    expect(tokens.scope).toContain('profile')
  })
})

/** The device-authorization document's shape, judged rather than asserted. */
function asCodes(
  value: unknown
): { user_code: string; device_code: string; verification_uri: string } | null {
  if (!isRecord(value)) return null
  const { user_code, device_code, verification_uri } = value
  if (
    typeof user_code !== 'string' ||
    typeof device_code !== 'string' ||
    typeof verification_uri !== 'string'
  )
    return null
  return { user_code, device_code, verification_uri }
}

/** The token document's shape, judged rather than asserted. */
function asTokens(value: unknown): { access_token?: string; scope?: string } | null {
  if (!isRecord(value)) return null
  const tokens: { access_token?: string; scope?: string } = {}
  if ('access_token' in value && typeof value.access_token === 'string')
    tokens.access_token = value.access_token
  if ('scope' in value && typeof value.scope === 'string') tokens.scope = value.scope
  return tokens
}
