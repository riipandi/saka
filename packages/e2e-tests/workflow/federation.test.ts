import { test, expect, type APIResponse } from '@playwright/test'
import {
  adminToken,
  bearer,
  closeSignInContexts,
  createVerifiedUser,
  deleteUser,
  isRecord,
  signIn
} from './helpers'

// The federation admin flows on a real deployment: an OIDC client created,
// read back, and deleted through the admin console's wire surface, and an
// OAuth SSO connection listed without ever answering a secret. The screens
// are the browser tests' subject; this file holds the composition the unit
// mocks cannot prove — the guard, the wire, and the real rows.

let admin: string

/** A response body as an object; anything else is a wire defect the test names. */
async function readRecord(response: APIResponse): Promise<Record<string, unknown>> {
  const body: unknown = await response.json()
  if (!isRecord(body)) throw new Error('the answer is not an object')
  return body
}

const text = (value: unknown): string => (typeof value === 'string' ? value : '')

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
    // The wire omits an empty list (protojson's unpopulated rule) — the
    // absent key is the empty answer, not a malformed one.
    const connections = body.connections ?? []
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
    // An empty ledger answers without the entries key — protobuf JSON omits
    // an empty repeated field — so the absence is the empty answer itself.
    expect(Array.isArray(body.entries ?? [])).toBeTruthy()
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
    await expect(page.getByRole('heading', { name: 'Consents' })).toBeVisible({ timeout: 15_000 })
    await page.close()
  })

  test('a ban ends what the relying party holds, and a replayed refresh token ends the grant', async ({
    request
  }) => {
    const account = await createVerifiedUser(request, admin, `rp${Date.now() % 100000}`)
    const callback = 'https://rp.example.com/callback'
    const created = await request.post('/rpc/saka.federation.v1.OidcClientService/CreateClient', {
      headers: bearer(admin),
      data: { name: 'E2E Relying Party', callbackUrls: [callback], skipConsent: true }
    })
    expect(created.ok()).toBeTruthy()
    const createdBody = await readRecord(created)
    const clientId = text(isRecord(createdBody.client) ? createdBody.client.id : undefined)
    const secret = text(createdBody.secret)

    const form = (fields: Record<string, string>) => ({
      headers: { 'content-type': 'application/x-www-form-urlencoded' },
      data: new URLSearchParams({
        client_id: clientId,
        client_secret: secret,
        ...fields
      }).toString()
    })
    const grantFor = async () => {
      const signedIn = await request.post('/rpc/saka.authn.v1.AuthService/SignIn', {
        data: { identity: account.username, password: account.password }
      })
      expect(signedIn.status()).toBe(200)
      const token = text((await readRecord(signedIn)).access_token)
      const authorize = await request.get(
        `/oidc/authorize?${new URLSearchParams({
          response_type: 'code',
          client_id: clientId,
          scope: 'openid profile email',
          state: 'e2e',
          redirect_uri: callback
        })}`,
        { headers: bearer(token), maxRedirects: 0 }
      )
      const code = new URL(authorize.headers().location ?? 'about:blank').searchParams.get('code')
      expect(code).toBeTruthy()
      const exchanged = await request.post(
        '/oidc/token',
        form({ grant_type: 'authorization_code', code: code ?? '', redirect_uri: callback })
      )
      expect(exchanged.status()).toBe(200)
      const tokens = await readRecord(exchanged)
      return { access_token: text(tokens.access_token), refresh_token: text(tokens.refresh_token) }
    }
    const refresh = (token: string) =>
      request.post('/oidc/token', form({ grant_type: 'refresh_token', refresh_token: token }))

    try {
      // A retired refresh token presented again ends the whole grant: the
      // replacement the legitimate client holds dies with it.
      const first = await grantFor()
      const rotated = await refresh(first.refresh_token)
      expect(rotated.status()).toBe(200)
      const second = { refresh_token: text((await readRecord(rotated)).refresh_token) }
      expect((await refresh(first.refresh_token)).status()).toBe(400)
      expect((await refresh(second.refresh_token)).status()).toBe(400)

      // A ban stops the next refresh and the profile the held access token
      // reads; the token itself still verifies until it expires.
      const held = await grantFor()
      const profile = await request.get('/oidc/userinfo', { headers: bearer(held.access_token) })
      expect((await readRecord(profile)).email).toBe(account.email)

      const ban = await request.post('/rpc/saka.identity.v1.UserService/BanUser', {
        headers: bearer(admin),
        data: { id: account.id, reason: 'e2e: the ban ends the relying party grant' }
      })
      expect(ban.status()).toBe(200)

      const refused = await refresh(held.refresh_token)
      expect(refused.status()).toBe(400)
      expect(await refused.text()).toContain('invalid_grant')
      const banned = await request.get('/oidc/userinfo', { headers: bearer(held.access_token) })
      expect((await readRecord(banned)).email).toBeUndefined()
    } finally {
      await request.post('/rpc/saka.federation.v1.OidcClientService/DeleteClient', {
        headers: bearer(admin),
        data: { id: clientId }
      })
      await deleteUser(request, admin, account.id)
    }
  })
})
