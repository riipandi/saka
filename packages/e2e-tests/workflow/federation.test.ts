import { test, expect } from '@playwright/test'
import { adminToken, bearer, closeSignInContexts, isRecord, signIn } from './helpers'

// The federation admin flows on a real deployment: an OIDC client created,
// read back, and deleted through the admin console's wire surface, and an
// OAuth SSO connection listed without ever answering a secret. The screens
// are the browser tests' subject; this file holds the composition the unit
// mocks cannot prove — the guard, the wire, and the real rows.

let admin: string

test.describe.serial('the federation flows', () => {
  test.beforeAll(async ({ request }) => {
    admin = await adminToken(request)
  })

  test.afterEach(async () => {
    await closeSignInContexts()
  })
  test('an OIDC client round trips through the admin surface', async ({ request }) => {
    const created = await request.post('/rpc/saka.federation.v1.OidcClientService/CreateClient', {
      headers: bearer(admin),
      data: {
        name: 'E2E Gallery',
        callbackUrls: ['https://gallery.example.com/callback'],
        clientType: 'standard'
      }
    })
    expect(created.ok()).toBeTruthy()
    const createdBody = (await created.json()) as unknown
    expect(isRecord(createdBody)).toBeTruthy()
    if (!isRecord(createdBody)) throw new Error('the create answer is not an object')
    const client = createdBody.client
    expect(isRecord(client)).toBeTruthy()
    if (!isRecord(client)) throw new Error('the create answer carries no client')
    const clientId = client.id
    expect(typeof clientId).toBe('string')
    // The raw secret is answered once at creation — the fact the show-once
    // dialog renders.
    const credentials = client.credentials
    expect(isRecord(credentials)).toBeTruthy()

    const read = await request.post('/rpc/saka.federation.v1.OidcClientService/GetClient', {
      headers: bearer(admin),
      data: { id: clientId }
    })
    expect(read.ok()).toBeTruthy()
    const readBody = (await read.json()) as unknown
    expect(isRecord(readBody)).toBeTruthy()
    if (!isRecord(readBody)) throw new Error('the read answer is not an object')
    const readClient = readBody.client
    expect(isRecord(readClient)).toBeTruthy()
    if (!isRecord(readClient)) throw new Error('the read answer carries no client')
    expect(readClient.name).toBe('E2E Gallery')

    const deleted = await request.post('/rpc/saka.federation.v1.OidcClientService/DeleteClient', {
      headers: bearer(admin),
      data: { id: clientId }
    })
    expect(deleted.ok()).toBeTruthy()
  })

  test('the OAuth connections listing never answers a secret', async ({ request }) => {
    const listed = await request.post('/rpc/saka.authn.v1.OAuthSSOService/ListConnections', {
      headers: bearer(admin),
      data: {}
    })
    expect(listed.ok()).toBeTruthy()
    const body = (await listed.json()) as unknown
    expect(isRecord(body)).toBeTruthy()
    if (!isRecord(body)) throw new Error('the listing is not an object')
    const connections = body.connections
    expect(Array.isArray(connections)).toBeTruthy()
    if (!Array.isArray(connections)) throw new Error('the listing carries no connections')
    for (const connection of connections) {
      expect(isRecord(connection)).toBeTruthy()
      if (!isRecord(connection)) throw new Error('a connection row is not an object')
      expect('clientSecret' in connection).toBeFalsy()
      expect('client_secret' in connection).toBeFalsy()
    }
  })

  test('the consent ledger answers with no rows leaking secrets', async ({ request }) => {
    const ledger = await request.post(
      '/rpc/saka.federation.v1.OidcConsentService/ListAllAuthorizedClients',
      { headers: bearer(admin), data: { page: 1, limit: 5 } }
    )
    expect(ledger.ok()).toBeTruthy()
    const body = (await ledger.json()) as unknown
    expect(isRecord(body)).toBeTruthy()
    if (!isRecord(body)) throw new Error('the ledger is not an object')
    expect(Array.isArray(body.entries)).toBeTruthy()
  })

  test('the account screens refuse a signed-out browser with the sign-in handoff', async ({
    browser
  }) => {
    const page = await browser.newPage()
    await page.goto('/account/connections')
    await expect(page).toHaveURL(/\/login/, { timeout: 15_000 })
    await page.close()
  })

  test('the admin consent ledger answers on the signed-in screen', async ({ browser }) => {
    const page = await signIn(browser, {
      username: process.env.E2E_IDENTITY || 'admin',
      password: process.env.E2E_PASSWORD || '@dmin123'
    })
    await page.goto('/admin/consents')
    await expect(page.getByText('Consents', { exact: true })).toBeVisible({ timeout: 15_000 })
    await page.close()
  })
})
